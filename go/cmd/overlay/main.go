package main

import (
	"context"
	"embed"
	"fmt"
	"os"
	"runtime"
	"sync/atomic"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/windows"
)

const windowTitle = "LLM Limits Overlay PoC"

//go:embed all:frontend/dist
var assets embed.FS

//go:embed app.ico
var appIcon []byte

var clickThrough atomic.Bool

func main() {
	if runtime.GOOS != "windows" && runtime.GOOS != "linux" {
		fmt.Println("PoC supports windows/linux only")
		os.Exit(1)
	}

	// Win32-хоткей + click-through (только Windows)
	if runtime.GOOS == "windows" {
		go runHotkeyLoop(windowTitle, &clickThrough)
	}

	// трей (fyne.io/systray) — в горутине
	go runTray()

	err := wails.Run(&options.App{
		Title:             windowTitle,
		Width:             290,
		Height:            402,
		Frameless:         true,
		AlwaysOnTop:       true,
		DisableResize:     true,
		WindowStartState:  options.Normal,
		BackgroundColour:  &options.RGBA{R: 0, G: 0, B: 0, A: 0},
		Assets:            &assets,
		Windows: &windows.Options{
			WindowIsTranslucent:               true,
			WebviewIsTransparent:              true,
			DisableWindowIcon:                 false,
			DisableFramelessWindowDecorations: true,
		},
		Linux: linuxOptions(),
		Bind:  []interface{}{},
		OnStartup: func(ctx context.Context) {
			setWailsContext(ctx)
			go positionBottomRight(ctx)
		},
	})
	if err != nil {
		fmt.Println("wails error:", err)
		os.Exit(1)
	}
}
