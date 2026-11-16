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

	// appVersion — единственный источник версии в логе и тултипе трея.
	appVersion = "2.2.2"

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
	isVisible        atomic.Bool // видимость окна: пишется только в UI-потоке (wndProc)
	isFetching       atomic.Bool
	isUserPositioned bool

	snapMu       sync.RWMutex
	lastSnapshot *model.Snapshot

	posX, posY    int32
	currentHeight int32 = defaultHeight
)

// WindowPosition — сохранённая позиция окна. Bottom — «закрепляемая» нижняя
// кромка: высота оверлея меняется (карточки схлопываются), и при старте окно
// должно расти/сжиматься вверх, оставляя нижнюю кромку на месте.
type WindowPosition struct {
	X      int `json:"x"`
	Y      int `json:"y"`
	Bottom int `json:"bottom,omitempty"`
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
	isVisible.Store(true)
}

// logAgyTimeout — фиксирует в логе эффективный таймаут agy: по этой строке
// проверяется, что конфиг/env подхватились (agyTimeout не дефолтный).
func logAgyTimeout() {
	src := "platform default"
	switch {
	case fetchers.AgyTimeoutFromEnv() > 0:
		src = "env LLM_LIMITS_AGY_TIMEOUT"
	case cfg != nil && cfg.AgyTimeoutSec >= 1 && cfg.AgyTimeoutSec <= 120:
		src = "overlay.config.json agyTimeout"
	}
	fetchers.LogLine(appDir, fmt.Sprintf("[overlay32] agy timeout %ds (%s)", agyTimeout(), src))
}

func checkSingleInstance() (uintptr, bool) {
	mutexName, _ := windows.UTF16PtrFromString("Local\\llm-limits-overlay32-instance-guid-52b36ded")
	hMutex, _, err := procCreateMutexW.Call(0, 0, uintptr(unsafe.Pointer(mutexName)))
	if hMutex != 0 && err == windows.ERROR_ALREADY_EXISTS {
		className, _ := windows.UTF16PtrFromString(windowClassName)
		hExisting, _, _ := procFindWindowW.Call(uintptr(unsafe.Pointer(className)), 0)
		if hExisting != 0 {
			// Не ShowWindow напрямую: работающий инстанс сам поднимет окно и
			// перерисовает его (иначе виден застывший кадр). Если он завис —
			// SendMessage/ShowWindow из чужого потока подвесил бы и нас.
			postMessage(hExisting, WM_USER_SHOW)
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
			// Приоритет у нижней кромки: окно «растёт вверх» от сохранённого низа.
			// Старый файл без bottom читается по верхней координате (как раньше).
			y := int32(pos.Y)
			if pos.Bottom > 0 {
				y = int32(pos.Bottom) - h
			}
			clampedX, clampedY := clampWindowPosition(int32(pos.X), y, windowWidth, h, wa)
			if clampedX != int32(pos.X) || clampedY != y {
				fetchers.LogLine(appDir, fmt.Sprintf("[overlay32] saved position (%d, %d) clamped to work area (%d, %d) [workArea: Left=%d Top=%d Right=%d Bottom=%d]", pos.X, y, clampedX, clampedY, wa.Left, wa.Top, wa.Right, wa.Bottom))
			}
			// Файл всегда хранит разрешённую позицию и актуальную нижнюю кромку.
			if clampedX != int32(pos.X) || clampedY != y {
				fetchers.LogLine(appDir, fmt.Sprintf("[overlay32] saved position bottom=%d -> y=%d (h=%d)", pos.Bottom, clampedY, h))
			}
			saveWindowPosition(clampedX, clampedY, h)
			return clampedX, clampedY
		}
	}

	isUserPositioned = false
	x, y := getDefaultPosition(h, wa)
	fetchers.LogLine(appDir, fmt.Sprintf("[overlay32] initial default position (%d, %d) [workArea: Left=%d Top=%d Right=%d Bottom=%d]", x, y, wa.Left, wa.Top, wa.Right, wa.Bottom))
	return x, y
}

func saveWindowPosition(x, y, h int32) {
	if x <= 0 && y <= 0 {
		return
	}
	pos := WindowPosition{X: int(x), Y: int(y), Bottom: int(y + h)}
	b, err := json.MarshalIndent(pos, "", "  ")
	if err != nil {
		return
	}
	posPath := filepath.Join(appDir, "window.pos.json")
	tmp := posPath + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		fetchers.LogLine(appDir, "[overlay32] save position failed: "+err.Error())
		return
	}
	if err := os.Rename(tmp, posPath); err != nil {
		fetchers.LogLine(appDir, "[overlay32] save position rename failed: "+err.Error())
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

func applyClickThrough(enable bool) {
	clickThrough.Store(enable)
	setClickThroughState(enable)
	updateTrayClickThrough(enable)
	if cfg != nil {
		cfg.SaveClickThrough(enable)
	}
	fetchers.LogLine(appDir, fmt.Sprintf("[overlay32] click-through = %v", enable))
}

func toggleClickThrough() {
	applyClickThrough(!clickThrough.Load())
}

func resetPosition() {
	isUserPositioned = false
	wa := getWorkArea()
	posX, posY = getDefaultPosition(currentHeight, wa)
	saveWindowPosition(posX, posY, currentHeight)
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
		// Закреплённая точка — нижняя кромка: она остаётся на месте,
		// верхняя уезжает вниз при схлопывании карточек и вверх при росте.
		bottom := posY + oldH
		posY = bottom - newH
		posX, posY = clampWindowPosition(posX, posY, windowWidth, newH, wa)
		saveWindowPosition(posX, posY, newH)
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
	fetchers.LogLine(appDir, fmt.Sprintf("[overlay32] resize h=%d->%d y=%d bottom=%d userPos=%v", oldH, newH, posY, posY+newH, isUserPositioned))
}

func requestRender() {
	postMessage(mainHWND, WM_USER_RENDER)
}

// postMessage — неблокирующая отправка сообщения окну из любого потока.
func postMessage(hwnd uintptr, msg uint32) bool {
	if hwnd == 0 {
		return false
	}
	r, _, _ := procPostMessageW.Call(hwnd, uintptr(msg), 0, 0)
	return r != 0
}

// requestToggleVisibility/requestToggleClickThrough — операции над окном
// запрашиваются из UI-потока: прямой ShowWindow и запись isVisible из горутины
// трея — гонка с wndProc, из-за которой окно оставалось скрытым.
func requestToggleVisibility() {
	postMessage(mainHWND, WM_USER_TOGGLE_VISIBILITY)
}

func requestToggleClickThrough() {
	postMessage(mainHWND, WM_USER_TOGGLE_CLICKTHROUGH)
}

func requestSyncVisibility() {
	postMessage(mainHWND, WM_USER_SYNC_VISIBILITY)
}

func requestResetPosition() {
	postMessage(mainHWND, WM_USER_RESET_POSITION)
}

func windowIsVisible() bool {
	if mainHWND == 0 {
		return false
	}
	r, _, _ := procIsWindowVisible.Call(mainHWND)
	return r != 0
}

// setVisibility — единственное место, где окно показывается/скрывается.
// Вызывается только из UI-потока (wndProc).
func setVisibility(visible bool, reason string) {
	if mainHWND == 0 {
		return
	}
	isVisible.Store(visible)
	if visible {
		procShowWindow.Call(mainHWND, SW_SHOW)
		requestRender()
	} else {
		procShowWindow.Call(mainHWND, SW_HIDE)
	}
	updateTrayVisibility(visible)
	fetchers.LogLine(appDir, fmt.Sprintf("[overlay32] visibility = %v (%s)", visible, reason))
}

func toggleVisibilityFromTray() {
	setVisibility(!isVisible.Load(), "tray")
}

// syncVisibility сверяет наше состояние с фактическим состоянием окна: окно
// могли показать снаружи (второй инстанс, ShowWindow из другого процесса) или
// скрыть в обход нас. Рассогласование — это «застывший» кадр или пропавший
// оверлей, поэтому лечим показом и принудительной перерисовкой.
func syncVisibility() {
	if mainHWND == 0 {
		return
	}
	desired := isVisible.Load()
	actual := windowIsVisible()
	if desired == actual {
		return
	}
	reason := "window was hidden externally"
	if actual {
		reason = "window was shown externally"
	}
	setVisibility(true, reason)
}

// agyTimeout — эффективный таймаут запуска agy: env LLM_LIMITS_AGY_TIMEOUT →
// overlay.config.json ("agyTimeout") → платформенный дефолт (Windows 25 с).
// Считается на каждый цикл, чтобы правка конфига подхватывалась без перезапуска
// процесса: agy.exe на Windows стартует 6–8 с, и жёсткие 8 с из 2.2.0 давали
// таймаут → fail-closed latch → замершую карточку AGY.
func agyTimeout() int {
	if cfg == nil {
		return fetchers.DefaultAgyTimeoutSec()
	}
	return fetchers.ResolveAgyTimeout(0, cfg.AgyTimeoutSec)
}

func triggerFetch(forceAgy bool) {
	if !isFetching.CompareAndSwap(false, true) {
		return
	}
	go func() {
		defer isFetching.Store(false)
		if forceAgy {
			fetchers.SetAgyBypassCache(true)
			// Bypass — только на один цикл. Раньше флаг оставался включённым до
			// перезапуска процесса: после первого «Обновить» кэш agy (TTL 300 с)
			// больше не читался и agy.exe запускался каждые 60 с.
			defer fetchers.SetAgyBypassCache(false)
			fetchers.LogLine(appDir, "[overlay32] force refresh requested (bypassing agy cache for this cycle only)")
		}

		opts := fetchers.Options{
			AppDir:        appDir,
			AgyTimeoutSec: agyTimeout(),
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
		saveWindowPosition(posX, posY, currentHeight)
		return 0

	case WM_HOTKEY:
		if wparam == HOTKEY_ID {
			toggleClickThrough()
		}
		return 0

	case WM_USER_TOGGLE_VISIBILITY:
		toggleVisibilityFromTray()
		return 0

	case WM_USER_TOGGLE_CLICKTHROUGH:
		toggleClickThrough()
		return 0

	case WM_USER_SHOW:
		// Просьба показаться (второй инстанс / внешний вызов).
		setVisibility(true, "show request")
		return 0

	case WM_USER_SYNC_VISIBILITY:
		syncVisibility()
		return 0

	case WM_USER_RESET_POSITION:
		resetPosition()
		return 0

	case WM_USER_RENDER:
		if isVisible.Load() {
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
	fetchers.LogLine(appDir, fmt.Sprintf("=== LLM Limits Overlay v%s Starting (PID=%d) ===", appVersion, os.Getpid()))
	logAgyTimeout()

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
	setVisibility(true, "startup")

	// Запуск трея
	go runTray()

	// Фоновый первый фетч
	triggerFetch(false)

	// Тикер на 10с для перерисовки относительного времени без сетевых запросов
	// и самовосстановления видимости окна (сверка с IsWindowVisible).
	go func() {
		ticker := time.NewTicker(10 * time.Second)
		defer ticker.Stop()
		for range ticker.C {
			requestRender()
			requestSyncVisibility()
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
