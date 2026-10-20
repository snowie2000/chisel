package main

import (
	"unsafe"

	"golang.org/x/sys/windows"
)

func desktopAvailable() bool { return true }

func detachConsole() {
	// Detach instead of hiding an inherited terminal belonging to the user.
	// This also handles ordinary console builds made without -H=windowsgui.
	windows.NewLazySystemDLL("kernel32.dll").NewProc("FreeConsole").Call()
}

func openBrowser(address string) error {
	verb, _ := windows.UTF16PtrFromString("open")
	target, err := windows.UTF16PtrFromString(address)
	if err != nil {
		return err
	}
	// No rundll32/cmd child and therefore no helper console window.
	return windows.ShellExecute(0, verb, target, nil, nil, windows.SW_SHOWNORMAL)
}

// Used only to report startup failures when no terminal is attached.
func showStartupError(err error) {
	message, _ := windows.UTF16PtrFromString(err.Error())
	title, _ := windows.UTF16PtrFromString("Chisel GUI")
	windows.NewLazySystemDLL("user32.dll").NewProc("MessageBoxW").Call(0, uintptr(unsafe.Pointer(message)), uintptr(unsafe.Pointer(title)), 0x10)
}
