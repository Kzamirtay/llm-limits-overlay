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
	wailsrt "github.com/wailsapp/wails/v2/pkg/runtime"
)

const windowTitle = "LLM Limits Overlay"

//go:embed all:frontend/dist
var assets embed.FS

//go:embed app.ico
var appIcon []byte

var clickThrough atomic.Bool

func main() {
	if runtime.GOOS != "windows" && runtime.GOOS != "linux" {
		fmt.Println("LLM Limits Overlay supports windows/linux only")
		os.Exit(1)
	}

	initAppDir()

	// Win32-хоткей + click-through (только Windows)
	if runtime.GOOS == "windows" {
		go runHotkeyLoop(windowTitle, &clickThrough)
	}

	// Трей (fyne.io/systray) в горутине
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
		SingleInstanceLock: &options.SingleInstanceLock{
			UniqueId: "llm-limits-overlay-instance-guid-52b36ded",
			OnSecondInstanceLaunch: func(secondInstanceData options.SecondInstanceData) {
				if wailsCtx != nil {
					wailsrt.WindowShow(wailsCtx)
					wailsrt.WindowUnminimise(wailsCtx)
				}
			},
		},
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
		},
		OnShutdown: func(ctx context.Context) {
			if wailsCtx != nil {
				x, y := wailsrt.WindowGetPosition(wailsCtx)
				saveWindowPosition(appDir, x, y)
			}
		},
	})
	if err != nil {
		fmt.Println("wails error:", err)
		os.Exit(1)
	}
}
