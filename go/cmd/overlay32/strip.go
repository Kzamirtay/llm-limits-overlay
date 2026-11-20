//go:build windows

package main

import (
	"fmt"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"

	"llmlimits/internal/fetchers"
	"llmlimits/internal/trayui"
)

// Полосы на панелях задач — дочерние слоистые окна самих панелей, слева от
// области уведомлений: «K 6%  O 19%  G 0%». Подход повторяет BatteryHub
// (github.com/krisgfx9/Wireless-Device-Battery-Monitor): окно WS_CHILD в
// Shell_TrayWnd (на Windows 11 — в Windows.UI.Composition.DesktopWindowContentBridge,
// на Windows 10 — в ReBarWindow32), отрисовка через UpdateLayeredWindow,
// пересчёт позиции раз в секунду.
//
// Почему дочернее окно, а не topmost-окно поверх панели: панель двигают,
// скрывают и масштабируют — дочернее окно едет вместе с ней, а topmost
// пришлось бы догонять по координатам и прятать при автопрятании.
const (
	stripClassName = "LLMLimitsStrip32Class"

	// Зазор до области уведомлений и внутренние поля полосы.
	stripGapRight = 2
	stripPadX     = 5

	WS_CHILD        = 0x40000000
	WS_CLIPSIBLINGS = 0x04000000
	SWP_SHOWWINDOW  = 0x0040
	HWND_TOP        = 0
	WS_EX_LAYOUTRTL = 0x00400000

	WM_LBUTTONUP_STRIP   = 0x0202
	WM_RBUTTONUP_STRIP   = 0x0205
	WM_CONTEXTMENU_STRIP = 0x007B
)

var (
	procFindWindowExW  = user32.NewProc("FindWindowExW")
	procGetClientRect  = user32.NewProc("GetClientRect")
	procClientToScreen = user32.NewProc("ClientToScreen")
	procSendMessageW   = user32.NewProc("SendMessageW")
	procIsWindow       = user32.NewProc("IsWindow")
	procGetWindowLongW = user32.NewProc("GetWindowLongW")
)

// taskbarStrip — полоса на одной панели задач (своя у каждой мониторной).
type taskbarStrip struct {
	bar    uintptr
	parent uintptr
	anchor uintptr // область уведомлений; 0 — панель без трея, полосу не ставим

	hwnd  uintptr
	hover bool

	line   *lineRenderer
	key    string
	width  int32
	height int32
	font   float32
}

var (
	// strips — по полосе на каждую горизонтальную панель задач (основная и
	// вторичные мониторы). Пусто = режим полосы недоступен.
	strips []*taskbarStrip
	// stripToolOwner — окно, с которым удалось зарегистрировать инструмент
	// подсказки (нужно для последующих TTM_UPDATETIPTEXTW).
	stripToolOwner uintptr
	stripToolSize  uint32
	stripOn        bool
	stripLastSum   trayui.Summary
)

// stripStart поднимает полосы на всех подходящих панелях задач. false —
// ни одной не нашлось (панели нет или она вертикальная): вызывающая сторона
// откатывается на значки трея.
func stripStart() bool {
	stripSync()
	if len(strips) == 0 {
		fetchers.LogLine(appDir, "[overlay32] strip: no horizontal taskbar found")
		return false
	}
	tipStart()
	stripOn = true
	return true
}

// stripSync приводит набор полос в соответствие с текущими панелями задач:
// создаёт недостающие (после смены мониторов или перезапуска explorer),
// снимает осиротевшие, обновляет позицию.
func stripSync() {
	bars := taskbarBars()
	alive := make(map[uintptr]bool, len(bars))
	next := make([]*taskbarStrip, 0, len(bars))

	for _, bar := range bars {
		alive[bar] = true
		var s *taskbarStrip
		for _, existing := range strips {
			if existing.bar == bar && isWindow(existing.hwnd) {
				s = existing
				break
			}
		}
		if s == nil {
			s = newStrip(bar)
			if s == nil {
				continue // панель без якоря (нет трея) — не угадываем позицию
			}
			fetchers.LogLine(appDir, fmt.Sprintf("[overlay32] strip ready (bar=%d parent=%d anchor=%d h=%d font=%.1f)",
				s.bar, s.parent, s.anchor, s.height, s.font))
		}
		if s.key != trayui.InlineKey(trayui.InlineSegments(stripLastSum)) {
			s.setContent(stripLastSum)
		}
		s.reposition()
		next = append(next, s)
	}

	for _, old := range strips {
		if !alive[old.bar] || !isWindow(old.hwnd) {
			old.close()
		}
	}
	strips = next
	stripOn = len(strips) > 0
}

// taskbarBars — основная панель задач и все вторичные (по монитору).
func taskbarBars() []uintptr {
	barName, _ := windows.UTF16PtrFromString("Shell_TrayWnd")
	// Второй аргумент FindWindowW (имя окна) обязателен: без него syscall
	// подставит в регистр мусор, и панель задач «не найдётся».
	main, _, _ := procFindWindowW.Call(uintptr(unsafe.Pointer(barName)), 0)
	bars := []uintptr{}
	if main != 0 && horizontalBar(main) {
		bars = append(bars, main)
	}
	secondaryName, _ := windows.UTF16PtrFromString("Shell_SecondaryTrayWnd")
	for prev := uintptr(0); ; {
		h, _, _ := procFindWindowExW.Call(0, prev, uintptr(unsafe.Pointer(secondaryName)), 0)
		if h == 0 {
			break
		}
		prev = h
		if horizontalBar(h) {
			bars = append(bars, h)
		}
	}
	return bars
}

// horizontalBar — полоса рисуется только на горизонтальной панели задач:
// вертикальную пришлось бы поворачивать, а её высота/ширина меняются местами.
func horizontalBar(bar uintptr) bool {
	var r RECT
	if ok, _, _ := procGetWindowRect.Call(bar, uintptr(unsafe.Pointer(&r))); ok == 0 {
		return false
	}
	w, h := r.Right-r.Left, r.Bottom-r.Top
	if w <= 0 || h <= 0 || w < h {
		return false
	}
	// RTL-панель (арабская/ивритская локаль) считается от правого края —
	// наши расчёты это не учитывают, поэтому такую панель пропускаем.
	if ex, _, _ := procGetWindowLongW.Call(bar, uintptr(GWL_EXSTYLE)); ex&WS_EX_LAYOUTRTL != 0 {
		return false
	}
	return true
}

// newStrip создаёт полосу на конкретной панели задач. nil — нет якоря
// (на вторичных панелях без трея позицию пришлось бы угадывать и можно
// закрыть часы или кнопки задач).
func newStrip(bar uintptr) *taskbarStrip {
	// Родитель — сама панель задач, а не её XAML-остров
	// (Windows.UI.Composition.DesktopWindowContentBridge): дочернее окно
	// острова рисуется, но мышь не получает — клики и подсказка не работают.
	parent := bar
	anchor := findVisibleChild(bar, "TrayNotifyWnd")
	if anchor == 0 {
		anchor = findVisibleChild(bar, "ClockButton")
	}
	if anchor == 0 {
		return nil
	}

	var barRect RECT
	procGetWindowRect.Call(bar, uintptr(unsafe.Pointer(&barRect)))

	hInst, _, _ := procGetModuleHandleW.Call(0)
	className, _ := windows.UTF16PtrFromString(stripClassName)
	wc := WNDCLASSEX{
		CbSize:        uint32(unsafe.Sizeof(WNDCLASSEX{})),
		LpfnWndProc:   syscall.NewCallback(stripWndProc),
		HInstance:     hInst,
		LpszClassName: className,
	}
	// Ошибка регистрации не фатальна: класс мог остаться от прошлой полосы.
	procRegisterClassExW.Call(uintptr(unsafe.Pointer(&wc)))

	empty, _ := windows.UTF16PtrFromString("")
	hwnd, _, err := procCreateWindowExW.Call(
		uintptr(WS_EX_LAYERED|WS_EX_NOACTIVATE),
		uintptr(unsafe.Pointer(className)),
		uintptr(unsafe.Pointer(empty)),
		uintptr(WS_CHILD|WS_CLIPSIBLINGS),
		0, 0, 1, 1,
		parent, 0, hInst, 0,
	)
	if hwnd == 0 {
		fetchers.LogLine(appDir, fmt.Sprintf("[overlay32] strip: CreateWindowExW failed: %v", err))
		return nil
	}

	s := &taskbarStrip{
		bar:    bar,
		parent: parent,
		anchor: anchor,
		hwnd:   hwnd,
		height: barRect.Bottom - barRect.Top,
	}
	// Размер шрифта привязан к высоте панели, а не к 96 DPI: при 125-150%
	// панель выше, и текст должен расти вместе с ней.
	s.font = stripFontSize(s.height)
	return s
}

func findChildByClass(parent uintptr, class string) uintptr {
	cls, err := windows.UTF16PtrFromString(class)
	if err != nil {
		return 0
	}
	h, _, _ := procFindWindowExW.Call(parent, 0, uintptr(unsafe.Pointer(cls)), 0)
	return h
}

// findVisibleChild — как findChildByClass, но берёт только видимого ребёнка:
// на вторичной панели трей может существовать скрытым.
func findVisibleChild(parent uintptr, class string) uintptr {
	for prev := uintptr(0); ; {
		cls, err := windows.UTF16PtrFromString(class)
		if err != nil {
			return 0
		}
		h, _, _ := procFindWindowExW.Call(parent, prev, uintptr(unsafe.Pointer(cls)), 0)
		if h == 0 {
			return 0
		}
		if vis, _, _ := procIsWindowVisible.Call(h); vis != 0 {
			return h
		}
		prev = h
	}
}

// stripWndProc: клик по полосе открывает то же меню, что было у значков трея.
func stripWndProc(hwnd uintptr, msg uint32, wparam, lparam uintptr) uintptr {
	switch msg {
	case WM_MOUSEMOVE_TIP:
		// Первое движение над полосой взводит таймер показа подсказки;
		// дальше мышь просто продолжает двигаться, таймер не перезапускаем.
		if s := stripByHWND(hwnd); s != nil && !s.hover {
			s.hover = true
			tipHoverStart(hwnd)
		}
		return 0

	case WM_MOUSELEAVE_TIP:
		if s := stripByHWND(hwnd); s != nil {
			s.hover = false
			tipHoverStop(hwnd)
		}
		return 0

	case WM_TIMER_TIP:
		if wparam != 1 {
			break
		}
		procKillTimer.Call(hwnd, 1)
		if s := stripByHWND(hwnd); s != nil {
			tip.show(s, stripLastSum)
		}
		return 0

	case WM_LBUTTONUP_STRIP, WM_RBUTTONUP_STRIP, WM_CONTEXTMENU_STRIP:
		tipHoverStop(hwnd)
		showTrayMenu(trayHWND)
		return 0
	}
	r, _, _ := procDefWindowProcW.Call(hwnd, uintptr(msg), wparam, lparam)
	return r
}

// stripUpdate принимает новую сводку: запоминает её и раскладывает по полосам.
// Вызывается из UI-потока после каждого цикла фетча.
func stripUpdate(sum trayui.Summary) {
	stripLastSum = sum
	if !stripOn {
		return
	}
	for _, s := range strips {
		s.setContent(sum)
		s.reposition()
	}
}

// stripTick — секундный тик: пересобрать набор полос (смена мониторов,
// перезапуск explorer) и пересчитать позиции.
func stripTick() {
	stripSync()
}

// setContent перерисовывает строку, если данные изменились.
func (s *taskbarStrip) setContent(sum trayui.Summary) {
	if s.hwnd == 0 {
		return
	}
	segs := trayui.InlineSegments(sum)
	key := trayui.InlineKey(segs)
	if key != s.key {
		if err := s.rebuild(segs); err != nil {
			fetchers.LogLine(appDir, fmt.Sprintf("[overlay32] strip: render failed: %v", err))
			return
		}
		s.key = key
	}
}

// rebuild пересоздаёт буфер под текущий текст и выводит его в окно.
func (s *taskbarStrip) rebuild(segs []trayui.InlineSegment) error {
	pal := paletteForTaskbar(taskbarIsLight())

	// Ширину меряем на временном рендерере: настоящий создаётся уже под
	// точный размер, иначе текст пришлось бы мерить дважды.
	probe, err := newLineRenderer(4096, s.height, s.font)
	if err != nil {
		return err
	}
	var textW float32
	for _, seg := range segs {
		if seg.Icon != "" {
			textW += iconRunWidth(s.font)
		}
		textW += probe.measure(seg.Text, seg.Style == trayui.InlineBold)
	}
	probe.destroy()

	width := int32(textW + stripPadX*2)
	if width < 20 {
		width = 20
	}
	line, err := newLineRenderer(width, s.height, s.font)
	if err != nil {
		return err
	}
	line.draw(segs, pal.Arc, stripPadX)
	if err := line.presentChild(s.hwnd); err != nil {
		line.destroy()
		return err
	}
	if s.line != nil {
		s.line.destroy()
	}
	s.line = line
	s.width = width
	return nil
}

// reposition ставит полосу вплотную к её области уведомлений.
func (s *taskbarStrip) reposition() {
	if s.hwnd == 0 || !isWindow(s.parent) {
		return
	}
	var barRect RECT
	procGetWindowRect.Call(s.bar, uintptr(unsafe.Pointer(&barRect)))
	barH := barRect.Bottom - barRect.Top
	if barH <= 0 || barRect.Right-barRect.Left < barH {
		return
	}
	if barH != s.height {
		// Панель стала выше/ниже (смена DPI или размера) — перерисовать.
		s.height = barH
		s.font = stripFontSize(barH)
		s.key = ""
	}

	anchorRight := int32(0)
	if s.anchor != 0 {
		var anchorRect RECT
		if r, _, _ := procGetWindowRect.Call(s.anchor, uintptr(unsafe.Pointer(&anchorRect))); r != 0 {
			anchorRight = anchorRect.Left
		}
	}
	if anchorRight == 0 {
		return
	}

	// Координаты дочернего окна — относительно клиентской области родителя.
	origin := POINT{}
	procClientToScreen.Call(s.parent, uintptr(unsafe.Pointer(&origin)))

	x := anchorRight - stripGapRight - origin.X - s.width
	if x < 0 {
		x = 0
	}
	// HWND_TOP без SWP_NOZORDER: панель задач (и её XAML-остров) периодически
	// поднимает своих детей, и полоса без принудительного поднятия оказывается
	// под ними — она видна, но клики и наведение мыши достаются острову.
	// Именно поэтому пересчёт позиции идёт раз в секунду, как у BatteryHub.
	procSetWindowPos.Call(
		s.hwnd, HWND_TOP,
		uintptr(x), 0,
		uintptr(s.width), uintptr(s.height),
		uintptr(SWP_NOACTIVATE|SWP_SHOWWINDOW),
	)
	if s.line != nil {
		s.line.presentChild(s.hwnd)
	}
}

// stripByHWND находит полосу по окну (в wndProc приходит только hwnd).
func stripByHWND(hwnd uintptr) *taskbarStrip {
	for _, s := range strips {
		if s.hwnd == hwnd {
			return s
		}
	}
	return nil
}

func (s *taskbarStrip) close() {
	tip.hide()
	if s.line != nil {
		s.line.destroy()
		s.line = nil
	}
	if s.hwnd != 0 {
		procDestroyWindow.Call(s.hwnd)
		s.hwnd = 0
	}
	s.key = ""
}

// stripStop снимает все полосы и подсказку при выходе.
func stripStop() {
	for _, s := range strips {
		s.close()
	}
	strips = nil
	stripOn = false
	tip.stop()
}

func isWindow(h uintptr) bool {
	if h == 0 {
		return false
	}
	r, _, _ := procIsWindow.Call(h)
	return r != 0
}

// stripFontSize — кегль под высоту панели задач. Панель задач Windows на
// 100% высотой 48 px и рисует часы кеглем ~12 px; при 125–150% высота растёт,
// и текст должен расти вместе с ней, иначе он выглядит мелким.
func stripFontSize(barHeight int32) float32 {
	return clampF(float32(barHeight)*14.0/48.0, 12, 22)
}

func clampF(v, lo, hi float32) float32 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}
