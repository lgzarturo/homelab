//go:build windows

package main

import (
	"os"

	"golang.org/x/sys/windows"
)

// Las consolas antiguas de Windows necesitan que se active el soporte de colores ANSI.
func init() {
	var modo uint32
	h := windows.Handle(os.Stdout.Fd())
	if windows.GetConsoleMode(h, &modo) == nil {
		_ = windows.SetConsoleMode(h, modo|windows.ENABLE_VIRTUAL_TERMINAL_PROCESSING)
	}
}
