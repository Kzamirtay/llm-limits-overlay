package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
	"unsafe"

	"llmlimits/internal/config"
	"llmlimits/internal/fetchers"
	"llmlimits/internal/model"

	"golang.org/x/sys/windows"
)

const (
	windowClassName = "LLMLimitsOverlay32Class"
	windowTitle     = "LLM Limits Overlay 32"
	windowWidth     = 290
	windowHeight    = 402

	ERROR_ALREADY_EXISTS = 183
)

var (
	procCreateMutexW = kernel32.NewProc("CreateMutexW")
	procFindWindowW  = user32.NewProc("FindWindowW")

	appDir       string
	cfg          *config.Config
	mainHWND     uintptr
	renderer     *Renderer
	clickThrough atomic.Bool
	isVisible    = true
	isFetching   atomic.Bool

	snapMu       sync.RWMutex
	lastSnapshot *model.Snapshot

	posX, posY int32
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
	fetchers.SetLogFileName("overlay32.log")
	cfgPath := filepath.Join(appDir, "overlay.config.json")
	cfg = config.Load(cfgPath)

	clickThrough.Store(cfg.ClickThrough)
}

func checkSingleInstance() (uintptr, bool) {
	mutexName, _ := windows.UTF16PtrFromString("Local\\llm-limits-overlay32-instance-guid-52b36ded")
	hMutex, _, err := procCreateMutexW.Call(0, 0, uintptr(unsafe.Pointer(mutexName)))
	if hMutex != 0 && err == windows.ERROR_ALREADY_EXISTS {
		className, _ := windows.UTF16PtrFromString(windowClassName)
		hExisting, _, _ := procFindWindowW.Call(uintptr(unsafe.Pointer(className)), 0)
		if hExisting != 0 {
			procShowWindow.Call(hExisting, SW_SHOW)
		}
		return 0, false
	}
	return hMutex, true
}

func loadWindowPosition() (int32, int32) {
	posPath := filepath.Join(appDir, "window.pos.json")
	raw, err := os.ReadFile(posPath)
	if err == nil {
		var pos WindowPosition
		if json.Unmarshal(raw, &pos) == nil && (pos.X != 0 || pos.Y != 0) {
			return int32(pos.X), int32(pos.Y)
		}
	}

	// Дефолт: правый-нижний угол основного экрана
	rW, _, _ := procGetSystemMetrics.Call(0) // SM_CXSCREEN
	rH, _, _ := procGetSystemMetrics.Call(1) // SM_CYSCREEN
	w := int32(rW)
	h := int32(rH)
	x := w - windowWidth - 14
	y := h - windowHeight - 4
	return x, y
}

func saveWindowPosition(x, y int32) {
	if x <= 0 && y <= 0 {
		return
	}
	pos := WindowPosition{X: int(x), Y: int(y)}
	b, err := json.MarshalIndent(pos, "", "  ")
	if err == nil {
		posPath := filepath.Join(appDir, "window.pos.json")
		tmp := posPath + ".tmp"
		if os.WriteFile(tmp, b, 0o644) == nil {
			os.Rename(tmp, posPath)
		}
	}
}

func setClickThroughState(enable bool) {
	if mainHWND == 0 {
		return
	}
	r, _, _ := procGetWindowLongPtrW.Call(mainHWND, GWL_EXSTYLE)
	style := uintptr(r)
	if enable {
		style |= WS_EX_TRANSPARENT | WS_EX_LAYERED
	} else {
		style &^= WS_EX_TRANSPARENT
	}
	procSetWindowLongPtrW.Call(mainHWND, GWL_EXSTYLE, style)
	procSetWindowPos.Call(mainHWND, 0, 0, 0, 0, 0, uintptr(SWP_NOSIZE|SWP_NOMOVE|SWP_NOZORDER|SWP_FRAMECHANGED))
}

func toggleClickThrough() {
	now := !clickThrough.Load()
	clickThrough.Store(now)
	setClickThroughState(now)
	updateTrayClickThrough(now)
	if cfg != nil {
		cfg.SaveClickThrough(now)
	}
	fetchers.LogLine(appDir, fmt.Sprintf("[overlay32] click-through = %v", now))
}

func toggleVisibility() {
	if mainHWND == 0 {
		return
	}
	isVisible = !isVisible
	if isVisible {
		procShowWindow.Call(mainHWND, SW_SHOW)
		requestRender()
	} else {
		procShowWindow.Call(mainHWND, SW_HIDE)
	}
	updateTrayVisibility(isVisible)
}

func resetPosition() {
	rW, _, _ := procGetSystemMetrics.Call(0)
	rH, _, _ := procGetSystemMetrics.Call(1)
	posX = int32(rW) - windowWidth - 14
	posY = int32(rH) - windowHeight - 4
	saveWindowPosition(posX, posY)
	if mainHWND != 0 {
		procSetWindowPos.Call(mainHWND, 0, uintptr(posX), uintptr(posY), 0, 0, uintptr(SWP_NOSIZE|SWP_NOZORDER|SWP_NOACTIVATE))
		requestRender()
	}
}

func requestRender() {
	if mainHWND != 0 {
		procPostMessageW.Call(mainHWND, WM_USER_RENDER, 0, 0)
	}
}

func triggerFetch(forceAgy bool) {
	if !isFetching.CompareAndSwap(false, true) {
		return
	}
	go func() {
		defer isFetching.Store(false)
		if forceAgy {
			fetchers.SetAgyBypassCache(true)
			fetchers.LogLine(appDir, "[overlay32] force refresh requested (bypassing agy cache)")
		}

		opts := fetchers.Options{
			AppDir:        appDir,
			AgyTimeoutSec: 8,
		}
		snap := fetchers.FetchAll(cfg, opts)
		if snap != nil {
			snapMu.Lock()
			lastSnapshot = snap
			snapMu.Unlock()
			requestRender()
		}
	}()
}

func wndProc(hwnd uintptr, msg uint32, wparam, lparam uintptr) uintptr {
	switch msg {
	case WM_NCHITTEST:
		// Если включён click-through, клики пропускаются дальше
		if clickThrough.Load() {
			return uintptr(HTCLIENT)
		}
		// Если обычный режим — drag за любое место оверлея
		return uintptr(HTCAPTION)

	case WM_MOVE:
		var rect RECT
		procGetWindowRect.Call(hwnd, uintptr(unsafe.Pointer(&rect)))
		posX = rect.Left
		posY = rect.Top
		return 0

	case WM_EXITSIZEMOVE:
		var rect RECT
		procGetWindowRect.Call(hwnd, uintptr(unsafe.Pointer(&rect)))
		posX = rect.Left
		posY = rect.Top
		saveWindowPosition(posX, posY)
		return 0

	case WM_HOTKEY:
		if wparam == HOTKEY_ID {
			toggleClickThrough()
		}
		return 0

	case WM_USER_RENDER:
		if renderer != nil && isVisible {
			snapMu.RLock()
			s := lastSnapshot
			snapMu.RUnlock()
			renderer.Render(s, cfg)
			renderer.Present(hwnd, posX, posY)
		}
		return 0

	case WM_DESTROY:
		procUnregisterHotKey.Call(hwnd, HOTKEY_ID)
		procPostQuitMessage.Call(0)
		return 0
	}

	r, _, _ := procDefWindowProcW.Call(hwnd, uintptr(msg), wparam, lparam)
	return r
}

func main() {
	runtime.LockOSThread()

	hMutex, ok := checkSingleInstance()
	if !ok {
		fmt.Println("Another instance is already running.")
		os.Exit(0)
	}
	defer windows.CloseHandle(windows.Handle(hMutex))

	initAppDir()
	fetchers.LogLine(appDir, fmt.Sprintf("=== LLM Limits Overlay32 Starting (PID=%d) ===", os.Getpid()))

	// GDI+ Startup
	var gdiplusToken uintptr
	gdiInput := GdiplusStartupInput{GdiplusVersion: 1}
	r, _, err := procGdiplusStartup.Call(uintptr(unsafe.Pointer(&gdiplusToken)), uintptr(unsafe.Pointer(&gdiInput)), 0)
	if r != 0 {
		fetchers.LogLine(appDir, fmt.Sprintf("[overlay32] GdiplusStartup failed: %v", err))
		os.Exit(1)
	}
	defer procGdiplusShutdown.Call(gdiplusToken)

	// Window Class
	className, _ := windows.UTF16PtrFromString(windowClassName)
	titleName, _ := windows.UTF16PtrFromString(windowTitle)
	hInst, _, _ := procGetModuleHandleW.Call(0)

	wc := WNDCLASSEX{
		CbSize:        uint32(unsafe.Sizeof(WNDCLASSEX{})),
		LpfnWndProc:   syscall.NewCallback(wndProc),
		HInstance:     hInst,
		LpszClassName: className,
	}
	procRegisterClassExW.Call(uintptr(unsafe.Pointer(&wc)))

	posX, posY = loadWindowPosition()

	exStyle := uintptr(WS_EX_LAYERED | WS_EX_TOOLWINDOW | WS_EX_TOPMOST)
	if clickThrough.Load() {
		exStyle |= WS_EX_TRANSPARENT
	}
	style := uintptr(WS_POPUP)

	hwnd, _, err := procCreateWindowExW.Call(
		exStyle,
		uintptr(unsafe.Pointer(className)),
		uintptr(unsafe.Pointer(titleName)),
		style,
		uintptr(posX), uintptr(posY),
		uintptr(windowWidth), uintptr(windowHeight),
		0, 0, hInst, 0,
	)
	if hwnd == 0 {
		fetchers.LogLine(appDir, fmt.Sprintf("[overlay32] CreateWindowExW failed: %v", err))
		os.Exit(1)
	}
	mainHWND = hwnd

	// После показа добавляем WS_EX_NOACTIVATE, чтобы оверлей не отбирал фокус
	rEx, _, _ := procGetWindowLongPtrW.Call(hwnd, GWL_EXSTYLE)
	procSetWindowLongPtrW.Call(hwnd, GWL_EXSTYLE, uintptr(rEx)|WS_EX_NOACTIVATE)

	// Регистрируем глобальный хоткей Ctrl+Alt+L
	rHot, _, errHot := procRegisterHotKey.Call(hwnd, HOTKEY_ID, MOD_CONTROL|MOD_ALT, VK_L)
	if rHot == 0 {
		fetchers.LogLine(appDir, fmt.Sprintf("[overlay32] RegisterHotKey Ctrl+Alt+L failed: %v", errHot))
	} else {
		fetchers.LogLine(appDir, "[overlay32] RegisterHotKey Ctrl+Alt+L registered")
	}

	// Инициализируем рендерер
	rnd, errRnd := NewRenderer(windowWidth, windowHeight)
	if errRnd != nil {
		fetchers.LogLine(appDir, fmt.Sprintf("[overlay32] NewRenderer failed: %v", errRnd))
		os.Exit(1)
	}
	renderer = rnd
	defer renderer.Destroy()

	// Загружаем кэшированные данные с диска
	snap := fetchers.LoadSnapshotFromDisk(cfg, appDir)
	if snap != nil {
		snapMu.Lock()
		lastSnapshot = snap
		snapMu.Unlock()
	}

	// Первый рендеринг
	renderer.Render(lastSnapshot, cfg)
	renderer.Present(hwnd, posX, posY)
	procShowWindow.Call(hwnd, SW_SHOW)

	// Запуск трея
	go runTray()

	// Фоновый первый фетч
	triggerFetch(false)

	// Тикер на 10с для перерисовки относительного времени
	go func() {
		ticker := time.NewTicker(10 * time.Second)
		defer ticker.Stop()
		for range ticker.C {
			requestRender()
		}
	}()

	// Тикер на 60с для периодического опроса квот
	go func() {
		ticker := time.NewTicker(60 * time.Second)
		defer ticker.Stop()
		for range ticker.C {
			triggerFetch(false)
		}
	}()

	// Главный цикл сообщений Win32
	var msg MSG
	for {
		ret, _, _ := procGetMessageW.Call(uintptr(unsafe.Pointer(&msg)), 0, 0, 0)
		if int32(ret) <= 0 {
			break
		}
		procTranslateMessage.Call(uintptr(unsafe.Pointer(&msg)))
		procDispatchMessageW.Call(uintptr(unsafe.Pointer(&msg)))
	}

	fetchers.LogLine(appDir, "=== LLM Limits Overlay32 Shutting Down ===")
}
