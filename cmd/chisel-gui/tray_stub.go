//go:build (!windows && !linux && !freebsd && !openbsd && !netbsd && !darwin) || (darwin && !cgo)

package main

import "log"

func runTray(address string, quit func(), finished <-chan struct{}) {
	log.Print("System tray unavailable in this build; use the browser to manage or quit Chisel GUI")
}
