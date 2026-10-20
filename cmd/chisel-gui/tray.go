//go:build windows || linux || freebsd || openbsd || netbsd || (darwin && cgo)

package main

import (
	"log"
	"runtime"

	"github.com/energye/systray"
)

// Run on the main OS thread (required by AppKit). HTTP, tunnel management and
// graceful shutdown run independently so the tray stays responsive.
func runTray(address string, quit func(), finished <-chan struct{}) {
	systray.Run(func() {
		systray.SetIcon(trayIcon(runtime.GOOS == "windows"))
		systray.SetTitle("Chisel")
		systray.SetTooltip("Chisel tunnel manager")
		open := func() {
			go func() {
				if err := openBrowser(address); err != nil {
					log.Printf("open admin page: %v", err)
				}
			}()
		}
		systray.AddMenuItem("Open admin page", "Manage connections in your browser").Click(open)
		systray.AddSeparator()
		systray.AddMenuItem("Quit Chisel GUI", "Disconnect all tunnels and exit").Click(quit)
		systray.SetOnDClick(func(systray.IMenu) { open() })
		log.Print("System tray ready")
		go func() { <-finished; systray.Quit() }()
	}, quit)
}
