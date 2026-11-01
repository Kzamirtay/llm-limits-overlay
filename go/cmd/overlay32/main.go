//go:build windows

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
	windowTitle     = "LLM Limits Overlay"
	windowWidth     = 290
	defaultHeight   = 402

	// Отступы оверлея от края рабочей области (work area)
	defaultMarginRight  = 12
	defaultMarginBottom = 12

	ERROR_ALREADY_EXISTS = 183
)

var (
	procCreateMutexW = kernel32.NewProc("CreateMutexW")
	procFindWindowW  = user32.NewProc("FindWindowW")

	appDir           string
	cfg              *config.Config
	mainHWND         uintptr
	renderer         *Renderer
	clickThrough     atomic.Bool
	isVisible        = true
	isFetching       atomic.Bool
	isUserPositioned bool

	snapMu       sync.RWMutex
	lastSnapshot *model.Snapshot

	posX, posY    int32
	currentHeight int32 = defaultHeight
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
	config.SetLogFunc(func(msg string) {
		fetchers.LogLine(appDir, msg)
	})
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

// getWorkArea возвращает рабочую область основного экрана (без панели задач)
func getWorkArea() RECT {
	var rc RECT
	r, _, _ := procSystemParametersInfoW.Call(
		uintptr(SPI_GETWORKAREA),
		0,
		uintptr(unsafe.Pointer(&rc)),
		0,
	)
	if r != 0 && (rc.Right > rc.Left) && (rc.Bottom > rc.Top) {
		return rc
	}
	// Фолбэк на SM_CXSCREEN / SM_CYSCREEN, если SystemParametersInfoW вернул ошибку
	rW, _, _ := procGetSystemMetrics.Call(0)
	rH, _, _ := procGetSystemMetrics.Call(1)
	return RECT{
		Left:   0,
		Top:    0,
		Right:  int32(rW),
		Bottom: int32(rH),
	}
}

// getDefaultPosition вычисляет стартовые координаты окна в правом нижнем углу рабочей области с отступами
func getDefaultPosition(h int32, wa RECT) (int32, int32) {
	x := wa.Right - windowWidth - defaultMarginRight
	y := wa.Bottom - h - defaultMarginBottom
	return x, y
}

// clampWindowPosition проверяет, находится ли окно полностью внутри рабочей области.
// Если позиция валидна — возвращает её без изменений.
// Если выходит за границы — клампит в рабочую область с учетом отступов.
func clampWindowPosition(x, y, w, h int32, wa RECT) (int32, int32) {
	// Валидная позиция: окно целиком помещается внутри рабочей области
	isValid := x >= wa.Left && (x+w) <= wa.Right && y >= wa.Top && (y+h) <= wa.Bottom
	if isValid {
		return x, y
	}

	minX := wa.Left + defaultMarginRight
	maxX := wa.Right - w - defaultMarginRight
	minY := wa.Top + defaultMarginBottom
	maxY := wa.Bottom - h - defaultMarginBottom

	if maxX < minX {
		maxX = minX
	}
	if maxY < minY {
		maxY = minY
	}

	if x < minX {
		x = minX
	} else if x > maxX {
		x = maxX
	}

	if y < minY {
		y = minY
	} else if y > maxY {
		y = maxY
	}

	return x, y
}

func loadWindowPosition(h int32) (int32, int32) {
	wa := getWorkArea()
	posPath := filepath.Join(appDir, "window.pos.json")
	raw, err := os.ReadFile(posPath)
	if err == nil {
		raw = config.StripBOM(raw)
		var pos WindowPosition
		if json.Unmarshal(raw, &pos) == nil && (pos.X != 0 || pos.Y != 0) {
			isUserPositioned = true
			clampedX, clampedY := clampWindowPosition(int32(pos.X), int32(pos.Y), windowWidth, h, wa)
			if clampedX != int32(pos.X) || clampedY != int32(pos.Y) {
				fetchers.LogLine(appDir, fmt.Sprintf("[overlay32] saved position (%d, %d) clamped to work area (%d, %d) [workArea: Left=%d Top=%d Right=%d Bottom=%d]", pos.X, pos.Y, clampedX, clampedY, wa.Left, wa.Top, wa.Right, wa.Bottom))
				saveWindowPosition(clampedX, clampedY)
			}
			return clampedX, clampedY
		}
	}

	isUserPositioned = false
	x, y := getDefaultPosition(h, wa)
	fetchers.LogLine(appDir, fmt.Sprintf("[overlay32] initial default position (%d, %d) [workArea: Left=%d Top=%d Right=%d Bottom=%d]", x, y, wa.Left, wa.Top, wa.Right, wa.Bottom))
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
	isUserPositioned = false
	wa := getWorkArea()
	posX, posY = getDefaultPosition(currentHeight, wa)
	saveWindowPosition(posX, posY)
	fetchers.LogLine(appDir, fmt.Sprintf("[overlay32] reset position to (%d, %d) [workArea: Left=%d Top=%d Right=%d Bottom=%d]", posX, posY, wa.Left, wa.Top, wa.Right, wa.Bottom))
	if mainHWND != 0 {
		procSetWindowPos.Call(mainHWND, 0, uintptr(posX), uintptr(posY), uintptr(windowWidth), uintptr(currentHeight), uintptr(SWP_NOZORDER|SWP_NOACTIVATE))
		requestRender()
	}
}

func applyWindowHeight(hwnd uintptr, newH int32) {
	if newH == currentHeight && renderer != nil {
		return
	}
	oldH := currentHeight
	currentHeight = newH

	wa := getWorkArea()
	if !isUserPositioned {
		posX, posY = getDefaultPosition(newH, wa)
	} else {
		posY = posY - (newH - oldH)
		posX, posY = clampWindowPosition(posX, posY, windowWidth, newH, wa)
	}

	if renderer != nil {
		renderer.Destroy()
		rnd, err := NewRenderer(windowWidth, newH)
		if err == nil {
			renderer = rnd
		} else {
			fetchers.LogLine(appDir, fmt.Sprintf("[overlay32] resize NewRenderer failed: %v", err))
		}
	}

	procSetWindowPos.Call(hwnd, 0, uintptr(posX), uintptr(posY), uintptr(windowWidth), uintptr(newH), uintptr(SWP_NOZORDER|SWP_NOACTIVATE))
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
		isUserPositioned = true
		saveWindowPosition(posX, posY)
		return 0

	case WM_HOTKEY:
		if wparam == HOTKEY_ID {
			toggleClickThrough()
		}
		return 0

	case WM_USER_RENDER:
		if isVisible {
			snapMu.RLock()
			s := lastSnapshot
			snapMu.RUnlock()

			targetH := CalculateWindowHeight(s, cfg)
			if targetH != currentHeight || renderer == nil {
				applyWindowHeight(hwnd, targetH)
			}

			if renderer != nil {
				renderer.Render(s, cfg)
				renderer.Present(hwnd, posX, posY)
			}
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
	fetchers.LogLine(appDir, fmt.Sprintf("=== LLM Limits Overlay v2.1.4 Starting (PID=%d) ===", os.Getpid()))

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

	// Загружаем кэшированные данные с диска перед созданием окна для точного расчёта высоты
	snap := fetchers.LoadSnapshotFromDisk(cfg, appDir)
	if snap != nil {
		snapMu.Lock()
		lastSnapshot = snap
		snapMu.Unlock()
	}

	currentHeight = CalculateWindowHeight(lastSnapshot, cfg)
	posX, posY = loadWindowPosition(currentHeight)

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
		uintptr(windowWidth), uintptr(currentHeight),
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
	rnd, errRnd := NewRenderer(windowWidth, currentHeight)
	if errRnd != nil {
		fetchers.LogLine(appDir, fmt.Sprintf("[overlay32] NewRenderer failed: %v", errRnd))
		os.Exit(1)
	}
	renderer = rnd
	defer renderer.Destroy()

	// Первый рендеринг
	renderer.Render(lastSnapshot, cfg)
	renderer.Present(hwnd, posX, posY)
	procShowWindow.Call(hwnd, SW_SHOW)

	// Запуск трея
	go runTray()

	// Фоновый первый фетч
	triggerFetch(false)

	// Тикер на 10с для перерисовки относительного времени без сетевых запросов
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
