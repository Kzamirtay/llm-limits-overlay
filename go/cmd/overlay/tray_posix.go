//go:build !windows

package main

import "fmt"

func toggleClickThroughFromTray() {
	fmt.Println("tray: click-through not supported on linux PoC")
}

func updateTrayClickThrough(enabled bool) {}
