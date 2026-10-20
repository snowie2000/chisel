// chisel-gui is an optional local UI for the Chisel library.
package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"flag"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"time"
)

func main() {
	if err := run(); err != nil {
		log.Print(err)
		if f := flag.Lookup("no-tray"); f != nil && f.Value.String() == "false" {
			showStartupError(err)
		}
		os.Exit(1)
	}
}

func run() error {
	listen := flag.String("listen", "127.0.0.1:0", "Loopback address for the local UI")
	noBrowser := flag.Bool("no-browser", false, "Do not open the default browser")
	noTray := flag.Bool("no-tray", false, "Disable the system tray and keep console output (headless/debug mode)")
	config := flag.String("config", "", "Profile file (defaults to the user config directory)")
	flag.Parse()
	if !*noTray {
		detachConsole()
	}
	if *config == "" {
		dir, err := os.UserConfigDir()
		if err != nil {
			return err
		}
		*config = filepath.Join(dir, "chisel-gui", "profiles.json")
	}
	host, _, err := net.SplitHostPort(*listen)
	if err != nil || net.ParseIP(host) == nil || !net.ParseIP(host).IsLoopback() {
		return errors.New("listen must use a loopback IP address")
	}
	a, err := newApp(*config)
	if err != nil {
		return err
	}
	ln, err := net.Listen("tcp", *listen)
	if err != nil {
		return err
	}
	defer ln.Close()
	a.host = ln.Addr().String()
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return err
	}
	a.token = hex.EncodeToString(b)
	srv := &http.Server{Handler: a.handler(), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second, WriteTimeout: 15 * time.Second, IdleTimeout: 60 * time.Second}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()
	a.startSaved()
	served := make(chan error, 1)
	go func() { served <- srv.Serve(ln) }()
	address := "http://" + a.host + "/#" + a.token
	log.Printf("Chisel GUI: %s", address)
	if !*noBrowser {
		if err := openBrowser(address); err != nil {
			log.Printf("Open the address above in your browser: %v", err)
		}
	}
	finished := make(chan struct{})
	var result error // Read only after finished closes.
	quit := func() { a.once.Do(func() { close(a.quit) }) }
	go func() {
		defer close(finished)
		select {
		case <-ctx.Done():
		case <-a.quit:
		case result = <-served:
		}
		a.stopAll()
		shutdown, done := context.WithTimeout(context.Background(), 5*time.Second)
		defer done()
		if err := srv.Shutdown(shutdown); result == nil {
			result = err
		}
	}()
	if !*noTray && desktopAvailable() {
		runTray(address, quit, finished)
	}
	<-finished
	return result
}
