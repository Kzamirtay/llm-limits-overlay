//go:build windows

package main

import "fmt"

func toggleClickThroughFromTray() {
	now := !clickThrough.Load()
	clickThrough.Store(now)
	hwnd := findOverlayHWND(windowTitle)
	setClickThrough(hwnd, now)
	updateTrayClickThrough(now)
	fmt.Println("tray: click-through =", now)
}

func updateTrayClickThrough(enabled bool) {
	if mClick != nil {
		if enabled {
			mClick.Check()
		} else {
			mClick.Uncheck()
		}
	}
}
