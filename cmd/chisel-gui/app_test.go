package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	chserver "github.com/snowie2000/chisel/server"
)

func testApp(t *testing.T) *app {
	t.Helper()
	a, err := newApp(filepath.Join(t.TempDir(), "profiles.json"))
	if err != nil {
		t.Fatal(err)
	}
	a.host = "127.0.0.1:9000"
	a.token = "test-token"
	t.Cleanup(a.stopAll)
	return a
}

func request(t *testing.T, a *app, method, path, token, origin, host string, body any) *httptest.ResponseRecorder {
	t.Helper()
	b, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest(method, "http://"+a.host+path, bytes.NewReader(b))
	r.Host = host
	r.Header.Set("X-Chisel-Token", token)
	r.Header.Set("Origin", origin)
	w := httptest.NewRecorder()
	a.handler().ServeHTTP(w, r)
	return w
}

func TestLocalAPIBoundary(t *testing.T) {
	a := testApp(t)
	for _, tc := range []struct {
		name, token, origin, host string
		want                      int
	}{
		{"authorized", a.token, "http://" + a.host, a.host, 200},
		{"missing token", "", "", a.host, 403},
		{"cross origin", a.token, "https://example.org", a.host, 403},
		{"DNS rebinding", a.token, "", "example.org", 403},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := request(t, a, "GET", "/api/state", tc.token, tc.origin, tc.host, nil)
			if w.Code != tc.want {
				t.Fatalf("got %d: %s", w.Code, w.Body)
			}
		})
	}
	w := request(t, a, "GET", "/", "", "", a.host, nil)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "Tunnel manager") || !strings.Contains(w.Header().Get("Content-Security-Policy"), "frame-ancestors 'none'") {
		t.Fatalf("invalid UI response: %d", w.Code)
	}
}

func TestProfilePersistenceAndSecrets(t *testing.T) {
	a := testApp(t)
	p := profile{ID: "home", Name: "Home", Server: "https://example.org", Remotes: []string{"127.0.0.1:8080:localhost:80"}, Retries: -1, Auth: "alice:password", Proxy: "http://alice:proxysecret@localhost:3128", Headers: "Authorization: Bearer private-token"}
	for cycle := 0; cycle < 3; cycle++ {
		w := request(t, a, "POST", "/api/save", a.token, "", a.host, p)
		if w.Code != 200 {
			t.Fatalf("save: %s", w.Body)
		}
		loaded, err := newApp(a.path)
		if err != nil {
			t.Fatal(err)
		}
		if len(loaded.profiles) != 1 {
			t.Fatal("save duplicated profile")
		}
		stored := loaded.profiles[0]
		if stored.Auth != p.Auth || stored.Headers != p.Headers || stored.Proxy != p.Proxy {
			t.Fatal("credentials did not survive restart")
		}
		if runtime.GOOS == "windows" {
			disk, err := os.ReadFile(a.path)
			if err != nil {
				t.Fatal(err)
			}
			for _, secret := range []string{"password", "proxysecret", "private-token"} {
				if bytes.Contains(disk, []byte(secret)) {
					t.Fatalf("plaintext credential %q stored on Windows", secret)
				}
			}
		}
		if a.profiles[0].Auth != p.Auth {
			t.Fatal("in-memory credentials were lost")
		}
	}
	// An unsuccessful disk write must not change the live profile list.
	if err := os.Mkdir(a.path+".directory", 0700); err != nil {
		t.Fatal(err)
	}
	a.path += ".directory"
	p.Name = "Changed"
	w := request(t, a, "POST", "/api/save", a.token, "", a.host, p)
	if w.Code == 200 || a.profiles[0].Name != "Home" {
		t.Fatal("failed save changed live state")
	}
}

func TestLegacyCredentialsMigration(t *testing.T) {
	a := testApp(t)
	// The old remember flag must not suppress credentials after migration.
	data := `[{"id":"legacy","name":"Legacy","server":"https://example.org","remotes":["8080"],"auth":"user:legacy-secret","proxy":"http://u:proxy-secret@localhost:3128","headers":"Authorization: Bearer legacy-header","remember":false}]`
	if err := os.WriteFile(a.path, []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
	loaded, err := newApp(a.path)
	if err != nil {
		t.Fatal(err)
	}
	if err := loaded.persist(loaded.profiles); err != nil {
		t.Fatal(err)
	}
	restarted, err := newApp(a.path)
	if err != nil {
		t.Fatal(err)
	}
	p := restarted.profiles[0]
	if p.Auth != "user:legacy-secret" || p.Proxy != "http://u:proxy-secret@localhost:3128" || p.Headers != "Authorization: Bearer legacy-header" {
		t.Fatal("legacy credentials were lost")
	}
}

func TestProfileValidation(t *testing.T) {
	base := profile{Name: "Test", Server: "example.org:8080", Remotes: []string{"127.0.0.1:8080:localhost:80"}, Retries: -1}
	if c, err := base.config(); err != nil || c.Server != "http://example.org:8080" || c.MaxRetryCount != -1 || c.KeepAlive != 25*time.Second {
		t.Fatalf("defaults: %+v, %v", c, err)
	}
	for _, change := range []func(*profile){
		func(p *profile) { p.Server = "file:///etc/passwd" },
		func(p *profile) { p.Server = "http://user:password@example.org" },
		func(p *profile) { p.Remotes = nil },
		func(p *profile) { p.Remotes = []string{"stdio:example.org:22"} },
		func(p *profile) { p.KeepAlive = "-1s" },
		func(p *profile) { p.MinRetry = "10m"; p.MaxRetry = "1s" },
		func(p *profile) { p.Proxy = "ftp://localhost" },
		func(p *profile) { p.Headers = "Broken header" },
	} {
		p := base
		change(&p)
		if _, err := p.config(); err == nil {
			t.Fatalf("accepted invalid profile: %+v", p)
		}
	}
}

func freeAddress(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := ln.Addr().String()
	ln.Close()
	return address
}

func TestTunnelLifecycle(t *testing.T) {
	// Exercise the same API the GUI uses against an authenticated, pinned Chisel
	// server, then verify real HTTP traffic through the tunnel and port release.
	a := testApp(t)
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, "through-chisel") }))
	defer backend.Close()
	server, err := chserver.NewServer(&chserver.Config{Auth: "alice:password"})
	if err != nil {
		t.Fatal(err)
	}
	server.SetOutput(io.Discard)
	relay := httptest.NewServer(server)
	defer relay.Close()
	local := freeAddress(t)
	p := profile{ID: "integration", Name: "Integration", Server: relay.URL, Remotes: []string{local + ":" + strings.TrimPrefix(backend.URL, "http://")}, Auth: "alice:password", Fingerprint: server.GetFingerprint(), Retries: -1}
	for cycle := 0; cycle < 2; cycle++ {
		for _, path := range []string{"save", "connect"} {
			w := request(t, a, "POST", "/api/"+path, a.token, "", a.host, p)
			if w.Code != 200 {
				t.Fatalf("%s: %s", path, w.Body)
			}
		}
		s := a.sessions[p.ID]
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		ready := s.client.Ready(ctx)
		cancel()
		if !ready {
			t.Fatal("client never became ready")
		}
		httpClient := &http.Client{Timeout: 3 * time.Second, Transport: &http.Transport{Proxy: nil, DisableKeepAlives: true}}
		resp, err := httpClient.Get("http://" + local)
		if err != nil {
			t.Fatal(err)
		}
		body, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil || string(body) != "through-chisel" {
			t.Fatalf("forwarded response %q: %v", body, err)
		}
		if w := request(t, a, "POST", "/api/connect", a.token, "", a.host, p); w.Code != 400 {
			t.Fatal("duplicate session accepted")
		}
		if w := request(t, a, "POST", "/api/delete", a.token, "", a.host, p); w.Code != 400 {
			t.Fatal("active session deleted")
		}
		if w := request(t, a, "POST", "/api/disconnect", a.token, "", a.host, p); w.Code != 200 {
			t.Fatal(w.Body)
		}
		select {
		case <-s.done:
		case <-time.After(5 * time.Second):
			t.Fatal("disconnect timed out")
		}
		ln, err := net.Listen("tcp", local)
		if err != nil {
			t.Fatalf("port was not released: %v", err)
		}
		ln.Close()
		s.mu.Lock()
		status := s.Status
		logs := strings.Join(s.Logs, "\n")
		s.mu.Unlock()
		if status != "disconnected" || !strings.Contains(logs, "Connected (Latency") {
			t.Fatalf("status=%s; logs=%s", status, logs)
		}
	}
}

func TestAutoConnectSavedProfiles(t *testing.T) {
	a := testApp(t)
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "automatically connected")
	}))
	defer backend.Close()
	server, err := chserver.NewServer(&chserver.Config{Auth: "alice:saved-password"})
	if err != nil {
		t.Fatal(err)
	}
	server.SetOutput(io.Discard)
	relay := httptest.NewServer(server)
	defer relay.Close()
	profiles := []profile{{ID: "invalid", Name: "Invalid profile", Server: "file:///invalid", Remotes: []string{"8080"}}}
	addresses := []string{freeAddress(t), freeAddress(t)}
	for i, address := range addresses {
		profiles = append(profiles, profile{ID: fmt.Sprintf("saved-%d", i), Name: "Saved connection", Server: relay.URL,
			Auth: "alice:saved-password", Fingerprint: server.GetFingerprint(), Retries: -1,
			Remotes: []string{address + ":" + strings.TrimPrefix(backend.URL, "http://")}})
	}
	if err := a.persist(profiles); err != nil {
		t.Fatal(err)
	}
	// Recreate the application to exercise credential restoration before startup.
	restarted, err := newApp(a.path)
	if err != nil {
		t.Fatal(err)
	}
	defer restarted.stopAll()
	restarted.startSaved()
	if s := restarted.sessions["invalid"]; s == nil || s.Status != "error" || !strings.Contains(strings.Join(s.Logs, "\n"), "Automatic connection failed") {
		t.Fatal("invalid profile's startup failure was not reported")
	}
	client := &http.Client{Timeout: 3 * time.Second, Transport: &http.Transport{Proxy: nil, DisableKeepAlives: true}}
	for i, address := range addresses {
		s := restarted.sessions[fmt.Sprintf("saved-%d", i)]
		if s == nil || s.client == nil {
			t.Fatal("saved profile was not started")
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		ready := s.client.Ready(ctx)
		cancel()
		if !ready {
			t.Fatal("saved profile did not authenticate automatically")
		}
		resp, err := client.Get("http://" + address)
		if err != nil {
			t.Fatal(err)
		}
		body, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil || string(body) != "automatically connected" {
			t.Fatalf("forwarded response: %q, %v", body, err)
		}
	}
}

func TestRetryExhaustion(t *testing.T) {
	a := testApp(t)
	p := profile{ID: "failure", Name: "Failure", Server: "http://" + freeAddress(t), Remotes: []string{freeAddress(t) + ":localhost:80"}, Retries: 0}
	if err := a.start(p); err != nil {
		t.Fatal(err)
	}
	s := a.sessions[p.ID]
	select {
	case <-s.done:
	case <-time.After(5 * time.Second):
		t.Fatal("retry exhaustion timed out")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.Status != "error" {
		t.Fatalf("status=%s, logs=%v", s.Status, s.Logs)
	}
}

func TestSessionLogRedactionAndBound(t *testing.T) {
	p := profile{Auth: "alice:login-secret", Proxy: "http://bob:proxy-secret@localhost:3128", Headers: "Authorization: Bearer header-secret"}
	s := &session{secrets: p.secrets()}
	s.Write([]byte("client: Connected (Latency 1ms)"))
	if s.Status != "connected" {
		t.Fatal("connected event was not observed")
	}
	for i := 0; i < 500; i++ {
		s.Write([]byte(p.Auth + " " + p.Proxy + " Bearer header-secret login-secret proxy-secret"))
	}
	if len(s.Logs) != 400 {
		t.Fatalf("log retained %d entries", len(s.Logs))
	}
	for _, line := range s.Logs {
		if strings.Contains(line, "secret") {
			t.Fatalf("secret appeared in log: %s", line)
		}
	}
	s.Write([]byte("client: Disconnected"))
	if s.Status != "reconnecting" {
		t.Fatal("disconnect event was not observed")
	}
}
