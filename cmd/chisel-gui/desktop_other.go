//go:build !windows

package main

import (
	"os"
	"os/exec"
	"runtime"
)

func detachConsole()             {}
func showStartupError(err error) {}

func desktopAvailable() bool {
	if runtime.GOOS == "darwin" {
		return true
	}
	return os.Getenv("DBUS_SESSION_BUS_ADDRESS") != "" && (os.Getenv("DISPLAY") != "" || os.Getenv("WAYLAND_DISPLAY") != "")
}

func openBrowser(address string) error {
	command := "xdg-open"
	if runtime.GOOS == "darwin" {
		command = "open"
	}
	cmd := exec.Command(command, address)
	if err := cmd.Start(); err != nil {
		return err
	}
	go cmd.Wait()
	return nil
}
