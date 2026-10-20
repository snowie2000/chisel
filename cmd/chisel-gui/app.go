package main

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	chclient "github.com/snowie2000/chisel/client"
	"github.com/snowie2000/chisel/share/settings"
	"golang.org/x/net/http/httpguts"
)

//go:embed web/*
var assets embed.FS

type profile struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Server      string   `json:"server"`
	Remotes     []string `json:"remotes"`
	Auth        string   `json:"auth"`
	Fingerprint string   `json:"fingerprint"`
	Proxy       string   `json:"proxy"`
	Headers     string   `json:"headers"`
	KeepAlive   string   `json:"keepAlive"`
	MinRetry    string   `json:"minRetry"`
	MaxRetry    string   `json:"maxRetry"`
	Retries     int      `json:"retries"`
	CA          string   `json:"ca"`
	Cert        string   `json:"cert"`
	Key         string   `json:"key"`
	ServerName  string   `json:"serverName"`
	SkipVerify  bool     `json:"skipVerify"`
	Verbose     bool     `json:"verbose"`
}

// Disk records remain compatible with the original JSON profile array.
type profileRecord struct {
	profile
	ProtectedSecrets string `json:"protectedSecrets,omitempty"`
}

func (p profile) config() (*chclient.Config, error) {
	server := strings.TrimSpace(p.Server)
	if !strings.Contains(server, "://") {
		server = "http://" + server
	}
	u, err := url.Parse(server)
	if err != nil || u.Hostname() == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.Fragment != "" {
		return nil, errors.New("enter an HTTP or HTTPS server URL without credentials or fragment")
	}
	if strings.TrimSpace(p.Name) == "" {
		return nil, errors.New("profile name is required")
	}
	if len(p.Remotes) == 0 {
		return nil, errors.New("add at least one tunnel")
	}
	for _, s := range p.Remotes {
		r, err := settings.DecodeRemote(s)
		if err != nil {
			return nil, fmt.Errorf("tunnel %q: %w", s, err)
		}
		if r.Stdio {
			return nil, errors.New("stdio tunnels require the CLI")
		}
	}
	if p.Retries < -1 {
		return nil, errors.New("retry count must be -1 (unlimited) or greater")
	}
	parse := func(s, fallback string) (time.Duration, error) {
		if s == "" {
			s = fallback
		}
		d, err := time.ParseDuration(s)
		if err != nil || d < 0 {
			return 0, fmt.Errorf("invalid duration %q", s)
		}
		return d, nil
	}
	keep, err := parse(p.KeepAlive, "25s")
	if err != nil {
		return nil, err
	}
	min, err := parse(p.MinRetry, "1s")
	if err != nil {
		return nil, err
	}
	max, err := parse(p.MaxRetry, "5m")
	if err != nil {
		return nil, err
	}
	if max < min {
		return nil, errors.New("maximum retry interval must be at least the minimum")
	}
	if p.Proxy != "" {
		v, err := url.Parse(p.Proxy)
		if err != nil || v.Hostname() == "" {
			return nil, errors.New("invalid proxy URL")
		}
		switch v.Scheme {
		case "http", "https", "socks", "socks5", "socks5h":
		default:
			return nil, errors.New("unsupported proxy scheme")
		}
	}
	headers := http.Header{}
	for _, line := range strings.Split(p.Headers, "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		key, value, ok := strings.Cut(line, ":")
		if !ok || !httpguts.ValidHeaderFieldName(strings.TrimSpace(key)) || !httpguts.ValidHeaderFieldValue(strings.TrimSpace(value)) {
			return nil, errors.New("headers must use Name: Value, one per line")
		}
		headers.Add(strings.TrimSpace(key), strings.TrimSpace(value))
	}
	return &chclient.Config{Server: server, Remotes: append([]string(nil), p.Remotes...), Auth: p.Auth, Fingerprint: p.Fingerprint, Proxy: p.Proxy, Headers: headers, KeepAlive: keep, MinRetryInterval: min, MaxRetryInterval: max, MaxRetryCount: p.Retries, TLS: chclient.TLSConfig{CA: p.CA, Cert: p.Cert, Key: p.Key, ServerName: p.ServerName, SkipVerify: p.SkipVerify}}, nil
}

func (p profile) secrets() []string {
	values := []string{p.Auth, p.Proxy}
	if _, password, ok := strings.Cut(p.Auth, ":"); ok {
		values = append(values, password)
	}
	if u, err := url.Parse(p.Proxy); err == nil && u.User != nil {
		password, _ := u.User.Password()
		values = append(values, password, url.QueryEscape(password))
	}
	for _, line := range strings.Split(p.Headers, "\n") {
		if _, value, ok := strings.Cut(line, ":"); ok {
			values = append(values, strings.TrimSpace(value))
		}
	}
	return values
}

type session struct {
	mu      sync.Mutex
	client  *chclient.Client
	cancel  context.CancelFunc
	done    chan struct{}
	Status  string    `json:"status"`
	Logs    []string  `json:"logs"`
	Started time.Time `json:"started"`
	secrets []string
}

func (s *session) Write(b []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	line := strings.TrimSpace(string(b))
	// The library exposes readiness but no disconnect subscription. Observe its
	// connection lifecycle messages without modifying the upstream logger.
	if strings.Contains(line, "client: Connected (Latency ") {
		s.Status = "connected"
	}
	if strings.Contains(line, "client: Disconnected") || strings.Contains(line, "client: Retrying in ") {
		s.Status = "reconnecting"
	}
	for _, secret := range s.secrets {
		if secret != "" {
			line = strings.ReplaceAll(line, secret, "[redacted]")
		}
	}
	s.Logs = append(s.Logs, line)
	if len(s.Logs) > 400 {
		s.Logs = s.Logs[len(s.Logs)-400:]
	}
	return len(b), nil
}

type app struct {
	mu                sync.Mutex
	profiles          []profile
	sessions          map[string]*session
	path, host, token string
	quit              chan struct{}
	once              sync.Once
}

func newApp(path string) (*app, error) {
	a := &app{path: path, profiles: []profile{}, sessions: map[string]*session{}, quit: make(chan struct{})}
	b, err := os.ReadFile(path)
	if err == nil {
		var records []profileRecord
		if err = json.Unmarshal(b, &records); err == nil {
			for _, record := range records {
				p, decodeErr := restoreProfile(record)
				if decodeErr != nil {
					return nil, fmt.Errorf("load credentials for %q: %w", record.Name, decodeErr)
				}
				a.profiles = append(a.profiles, p)
			}
		}
	}
	if err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("load profiles: %w", err)
	}
	return a, nil
}

// persist saves complete profiles. Windows seals credentials with user-scoped
// DPAPI before writing; other platforms use the private profile file.
func (a *app) persist(profiles []profile) error {
	safe := make([]profileRecord, 0, len(profiles))
	for _, p := range profiles {
		record, err := protectProfile(p)
		if err != nil {
			return fmt.Errorf("save credentials: %w", err)
		}
		safe = append(safe, record)
	}
	b, err := json.MarshalIndent(safe, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(a.path), 0700); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(a.path), ".profiles-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if err = f.Chmod(0600); err == nil {
		_, err = f.Write(b)
	}
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	return os.Rename(f.Name(), a.path)
}

// startSaved runs once on application startup, after credentials are restored.
// Each client connects asynchronously and handles retries independently.
func (a *app) startSaved() {
	a.mu.Lock()
	defer a.mu.Unlock()
	for _, p := range a.profiles {
		if err := a.start(p); err != nil {
			// Keep a failed profile editable and show its failure in the UI while
			// allowing the remaining profiles to connect.
			s := &session{Status: "error", Logs: []string{}, cancel: func() {}, done: make(chan struct{}), secrets: p.secrets()}
			s.Write([]byte("Automatic connection failed: " + err.Error()))
			close(s.done)
			a.sessions[p.ID] = s
		}
	}
}

func (a *app) start(p profile) error {
	if s := a.sessions[p.ID]; s != nil {
		select {
		case <-s.done:
		default:
			return errors.New("disconnect this profile before connecting again")
		}
	}
	cfg, err := p.config()
	if err != nil {
		return err
	}
	c, err := chclient.NewClient(cfg)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithCancel(context.Background())
	s := &session{client: c, cancel: cancel, done: make(chan struct{}), Status: "connecting", Logs: []string{}, Started: time.Now(), secrets: p.secrets()}
	c.SetOutput(s)
	c.Debug = p.Verbose
	if err := c.Start(ctx); err != nil {
		cancel()
		return err
	}
	a.sessions[p.ID] = s
	go func() {
		defer close(s.done)
		defer cancel()
		err := c.Wait()
		if err != nil {
			s.Write([]byte(err.Error()))
		}
		s.mu.Lock()
		defer s.mu.Unlock()
		if ctx.Err() == nil && err != nil {
			s.Status = "error"
		} else {
			s.Status = "disconnected"
		}
	}()
	return nil
}

func (a *app) stopAll() {
	a.mu.Lock()
	defer a.mu.Unlock()
	for _, s := range a.sessions {
		s.cancel()
	}
	deadline := time.After(5 * time.Second)
	for _, s := range a.sessions {
		select {
		case <-s.done:
		case <-deadline:
			return
		}
	}
}

func (a *app) handler() http.Handler {
	sub, _ := fs.Sub(assets, "web")
	files := http.FileServer(http.FS(sub))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self'; frame-ancestors 'none'; base-uri 'none'; form-action 'self'")
		if r.Host != a.host {
			http.Error(w, "invalid host", http.StatusForbidden)
			return
		}
		if strings.HasPrefix(r.URL.Path, "/api/") {
			if r.Header.Get("X-Chisel-Token") != a.token || (r.Header.Get("Origin") != "" && r.Header.Get("Origin") != "http://"+a.host) {
				http.Error(w, "unauthorized", http.StatusForbidden)
				return
			}
			a.api(w, r)
			return
		}
		files.ServeHTTP(w, r)
	})
}

func (a *app) api(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	a.mu.Lock()
	defer a.mu.Unlock()
	if r.Method == "GET" && r.URL.Path == "/api/state" {
		states := map[string]any{}
		for id, s := range a.sessions {
			s.mu.Lock()
			states[id] = map[string]any{"status": s.Status, "logs": append([]string{}, s.Logs...), "started": s.Started}
			s.mu.Unlock()
		}
		json.NewEncoder(w).Encode(map[string]any{"profiles": a.profiles, "sessions": states})
		return
	}
	if r.Method != "POST" {
		http.Error(w, "method not allowed", 405)
		return
	}
	var p profile
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&p); err != nil {
		http.Error(w, "invalid request", 400)
		return
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		http.Error(w, "invalid request", 400)
		return
	}
	var err error
	switch r.URL.Path {
	case "/api/save":
		if p.ID == "" || len(p.ID) > 100 {
			err = errors.New("invalid profile ID")
			break
		}
		if _, err = p.config(); err != nil {
			break
		}
		next := append([]profile{}, a.profiles...)
		found := false
		for i := range next {
			if next[i].ID == p.ID {
				next[i] = p
				found = true
			}
		}
		if !found {
			next = append(next, p)
		}
		if err = a.persist(next); err == nil {
			a.profiles = next
		}
	case "/api/connect":
		found := false
		for _, saved := range a.profiles {
			if saved.ID == p.ID {
				found = true
				err = a.start(saved)
				break
			}
		}
		if !found {
			err = errors.New("save the profile before connecting")
		}
	case "/api/disconnect":
		if s := a.sessions[p.ID]; s != nil {
			s.cancel()
		}
	case "/api/delete":
		if s := a.sessions[p.ID]; s != nil {
			select {
			case <-s.done:
			default:
				err = errors.New("disconnect before deleting")
			}
		}
		if err != nil {
			break
		}
		next := []profile{}
		for _, saved := range a.profiles {
			if saved.ID != p.ID {
				next = append(next, saved)
			}
		}
		if err = a.persist(next); err == nil {
			a.profiles = next
			delete(a.sessions, p.ID)
		}
	case "/api/quit":
		a.once.Do(func() { close(a.quit) })
	default:
		http.NotFound(w, r)
		return
	}
	if err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	io.WriteString(w, "{}")
}
