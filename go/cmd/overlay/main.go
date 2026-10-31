package main

import (
	"context"
	"embed"
	"fmt"
	"os"
	"runtime"
	"sync/atomic"

	"llmlimits/internal/fetchers"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/logger"
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

type overlayLogger struct {
	appDir string
}

func (l *overlayLogger) Print(message string)   { fetchers.LogLine(l.appDir, "[wails] "+message) }
func (l *overlayLogger) Trace(message string)   { fetchers.LogLine(l.appDir, "[wails:trace] "+message) }
func (l *overlayLogger) Debug(message string)   { fetchers.LogLine(l.appDir, "[wails:debug] "+message) }
func (l *overlayLogger) Info(message string)    { fetchers.LogLine(l.appDir, "[wails:info] "+message) }
func (l *overlayLogger) Warning(message string) { fetchers.LogLine(l.appDir, "[wails:warn] "+message) }
func (l *overlayLogger) Error(message string)   { fetchers.LogLine(l.appDir, "[wails:error] "+message) }
func (l *overlayLogger) Fatal(message string)   { fetchers.LogLine(l.appDir, "[wails:fatal] "+message) }

func main() {
	if runtime.GOOS != "windows" && runtime.GOOS != "linux" {
		fmt.Println("LLM Limits Overlay supports windows/linux only")
		os.Exit(1)
	}

	initAppDir()
	fetchers.LogLine(appDir, fmt.Sprintf("=== LLM Limits Overlay Starting (PID=%d) ===", os.Getpid()))

	// Диагностика доступности WebView2 до старта Wails
	checkWebView2Runtime(appDir)

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
		Logger:            &overlayLogger{appDir: appDir},
		LogLevel:          logger.INFO,
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
			fetchers.LogLine(appDir, "[wails] WebView2 initialized and window created.")
			setWailsContext(ctx)
		},
		OnDomReady: func(ctx context.Context) {
			fetchers.LogLine(appDir, "[wails] WebView2 DOM ready.")
		},
		OnShutdown: func(ctx context.Context) {
			fetchers.LogLine(appDir, "=== LLM Limits Overlay Shutting Down ===")
			if wailsCtx != nil {
				x, y := wailsrt.WindowGetPosition(wailsCtx)
				saveWindowPosition(appDir, x, y)
			}
		},
	})
	if err != nil {
		msg := fmt.Sprintf("[wails] fatal startup error: %v", err)
		fetchers.LogLine(appDir, msg)
		fmt.Println(msg)
		os.Exit(1)
	}
}
