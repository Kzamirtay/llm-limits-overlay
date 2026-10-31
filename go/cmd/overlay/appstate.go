package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"sync/atomic"
	"time"

	"llmlimits/internal/config"
	"llmlimits/internal/fetchers"
	wailsrt "github.com/wailsapp/wails/v2/pkg/runtime"
)

var (
	wailsCtx    context.Context
	appDir      string
	cfg         *config.Config
	isFetching  atomic.Bool
	isVisible   = true

	payloadMu     sync.RWMutex
	latestPayload OverlayPayload

	lastSavedX = -99999
	lastSavedY = -99999
)

type WindowPosition struct {
	X int `json:"x"`
	Y int `json:"y"`
}

func initAppDir() {
	exe, err := os.Executable()
	if err != nil {
		exe = os.Args[0]
	}
	appDir = filepath.Dir(exe)
	cfgPath := filepath.Join(appDir, "overlay.config.json")
	cfg = config.Load(cfgPath)

	// Начальный статус click-through из конфига
	clickThrough.Store(cfg.ClickThrough)
}

func setWailsContext(ctx context.Context) {
	wailsCtx = ctx

	// Слушаем событие готовности UI для отправки кэшированных данных
	wailsrt.EventsOn(ctx, "ui-ready", func(data ...interface{}) {
		sendCurrentState()
	})

	wailsrt.EventsOn(ctx, "request-refresh", func(data ...interface{}) {
		go doFetch(true)
	})

	wailsrt.EventsOn(ctx, "resize-window", func(data ...interface{}) {
		if len(data) > 0 {
			if h, ok := data[0].(float64); ok && h > 100 {
				wailsrt.WindowSetSize(ctx, 290, int(h))
			}
		}
	})

	// Загружаем кэш с диска и сразу отображаем
	loadInitialData()

	// Запускаем позиционирование (с учётом сохранённой позиции)
	restoreOrInitPosition(ctx)

	// Watchdog сохранения позиции при перетаскивании
	go watchPosition(ctx)

	// Первый фоновый фетч
	go doFetch(false)

	// Периодический таймер фетча (раз в 60с)
	go startPeriodicFetch()
}

func sendCurrentState() {
	payloadMu.RLock()
	p := latestPayload
	payloadMu.RUnlock()

	if wailsCtx != nil && len(p.Providers) > 0 {
		wailsrt.EventsEmit(wailsCtx, "quota-update", p)
	}
}

func loadInitialData() {
	snap := fetchers.LoadSnapshotFromDisk(cfg, appDir)
	if snap != nil {
		payloadMu.Lock()
		latestPayload = buildOverlayPayload(snap, cfg)
		payloadMu.Unlock()
		fetchers.LogLine(appDir, fmt.Sprintf("Cache loaded successfully from limits.json (status: %s).", snap.Status))
		sendCurrentState()
	}
}

func doFetch(forceAgy bool) {
	if !isFetching.CompareAndSwap(false, true) {
		fetchers.LogLine(appDir, "Fetch already in progress, skipping trigger.")
		return
	}
	defer isFetching.Store(false)

	if forceAgy {
		fetchers.SetAgyBypassCache(true)
		fetchers.LogLine(appDir, "Force refresh requested from UI/Tray (bypassing agy cache).")
	}

	agyTimeout := 20
	if runtime.GOOS == "windows" {
		agyTimeout = 8
	}

	opts := fetchers.Options{
		AppDir:        appDir,
		AgyTimeoutSec: agyTimeout,
	}

	snap := fetchers.FetchAll(cfg, opts)
	if snap != nil {
		payloadMu.Lock()
		latestPayload = buildOverlayPayload(snap, cfg)
		payloadMu.Unlock()
		sendCurrentState()
	}
}

func startPeriodicFetch() {
	ticker := time.NewTicker(60 * time.Second)
	defer ticker.Stop()
	for range ticker.C {
		doFetch(false)
	}
}

func restoreOrInitPosition(ctx context.Context) {
	posPath := filepath.Join(appDir, "window.pos.json")
	raw, err := os.ReadFile(posPath)
	if err == nil {
		var pos WindowPosition
		if json.Unmarshal(raw, &pos) == nil && (pos.X != 0 || pos.Y != 0) {
			wailsrt.WindowSetPosition(ctx, pos.X, pos.Y)
			lastSavedX, lastSavedY = pos.X, pos.Y
			fmt.Printf("position: restored from window.pos.json -> (%d,%d)\n", pos.X, pos.Y)
			initPlatformWindow(windowTitle)
			return
		}
	}

	// Дефолт: правый-нижний угол основного экрана
	positionBottomRight(ctx)
}

func watchPosition(ctx context.Context) {
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()
	for range ticker.C {
		if ctx == nil {
			continue
		}
		x, y := wailsrt.WindowGetPosition(ctx)
		if x != lastSavedX || y != lastSavedY {
			lastSavedX, lastSavedY = x, y
			saveWindowPosition(appDir, x, y)
		}
	}
}

func saveWindowPosition(dir string, x, y int) {
	pos := WindowPosition{X: x, Y: y}
	data, err := json.MarshalIndent(pos, "", "  ")
	if err == nil {
		_ = os.WriteFile(filepath.Join(dir, "window.pos.json"), data, 0644)
	}
}

func toggleVisibility() {
	if wailsCtx == nil {
		return
	}
	if isVisible {
		wailsrt.WindowHide(wailsCtx)
		isVisible = false
		if mToggle != nil {
			mToggle.SetTitle("Показать оверлей")
		}
	} else {
		wailsrt.WindowShow(wailsCtx)
		isVisible = true
		if mToggle != nil {
			mToggle.SetTitle("Скрыть оверлей")
		}
	}
	fmt.Println("tray: visible =", isVisible)
}

func resetPosition() {
	if wailsCtx == nil {
		return
	}
	positionBottomRight(wailsCtx)
	x, y := wailsrt.WindowGetPosition(wailsCtx)
	saveWindowPosition(appDir, x, y)
}
