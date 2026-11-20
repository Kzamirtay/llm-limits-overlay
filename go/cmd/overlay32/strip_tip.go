//go:build windows

package main

import (
	"fmt"
	"math"
	"strings"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"

	"llmlimits/internal/fetchers"
	"llmlimits/internal/trayui"
)

// Своя подсказка вместо системной: нативная в этой сборке остаётся
// классической (без манифеста Common Controls 6.0 она не темуется и выглядит
// жёлтым прямоугольником из Windows 95). Здесь — слоистое окно с той же
// «матовой» карточкой, что и HUD: тот же GDI+, те же цвета и скругления.
const (
	tipClassName = "LLMLimitsTip32Class"

	tipHoverDelayMs = 450
	tipPadX         = 12
	tipPadY         = 9
	tipLineGap      = 4
	tipRadius       = 8
	tipOffset       = 6 // зазор между полосой и подсказкой

	// Mouse/timer messages
	WM_MOUSEMOVE_TIP  = 0x0200
	WM_MOUSELEAVE_TIP = 0x02A3
	WM_TIMER_TIP      = 0x0113
	TME_LEAVE         = 0x00000002
)

var (
	procSetTimer       = user32.NewProc("SetTimer")
	procKillTimer      = user32.NewProc("KillTimer")
	procTrackMouseEvnt = user32.NewProc("TrackMouseEvent")
)

// TRACKMOUSEEVENT — чтобы получить WM_MOUSELEAVE и спрятать подсказку.
type TRACKMOUSEEVENT struct {
	CbSize      uint32
	DwFlags     uint32
	HwndTrack   uintptr
	DwHoverTime uint32
}

// Подсказка повторяет палитру карточек HUD (render.go): полупрозрачная
// светлая плашка, тёмный заголовок, приглушённые значения.
var (
	colorTipFill   = argb(246, 238, 242, 246)
	colorTipBorder = argb(40, 100, 116, 139)
	colorTipShadow = argb(24, 0, 0, 0)
	colorTipName   = argb(255, 0, 0, 0)
	colorTipValue  = argb(255, 15, 23, 42)
	// Остаток до сброса — приглушённый, как meta-текст на карточках HUD.
	colorTipReset = argb(255, 71, 85, 105)
)

// tipInk — цвет иконок в подсказке (в тон имени провайдера).
var tipInk = trayui.RGB{R: 0x00, G: 0x00, B: 0x00}

// tipCell — окно провайдера: «7д 9%» и остаток до сброса «3ч 19м».
type tipCell struct {
	Main  string
	Reset string
}

// tipLine — строка подсказки: иконка провайдера, имя жирным и его окна.
type tipLine struct {
	Icon  string
	Name  string
	Cells []tipCell
}

// cellText — вся ячейка строкой (для ключа и измерений).
func (c tipCell) text() string {
	if c.Reset == "" {
		return c.Main
	}
	return c.Main + " · " + c.Reset
}

// tipCellSeparator — промежуток между окнами одного провайдера.
const tipCellSeparator = "   "

// hudTip — окно подсказки. Одно на все полосы: показывается у той, на которую
// навели мышь.
type hudTip struct {
	hwnd   uintptr
	line   *lineRenderer
	key    string
	width  int32
	height int32
	font   float32
}

var tip hudTip

// tipStart создаёт скрытое окно подсказки (topmost, без активации).
func tipStart() {
	if tip.hwnd != 0 {
		return
	}
	hInst, _, _ := procGetModuleHandleW.Call(0)
	className, _ := windows.UTF16PtrFromString(tipClassName)
	wc := WNDCLASSEX{
		CbSize:        uint32(unsafe.Sizeof(WNDCLASSEX{})),
		LpfnWndProc:   syscall.NewCallback(tipWndProc),
		HInstance:     hInst,
		LpszClassName: className,
	}
	procRegisterClassExW.Call(uintptr(unsafe.Pointer(&wc)))

	empty, _ := windows.UTF16PtrFromString("")
	hwnd, _, err := procCreateWindowExW.Call(
		uintptr(WS_EX_LAYERED|WS_EX_NOACTIVATE|WS_EX_TOOLWINDOW|WS_EX_TOPMOST),
		uintptr(unsafe.Pointer(className)),
		uintptr(unsafe.Pointer(empty)),
		uintptr(WS_POPUP),
		0, 0, 1, 1,
		0, 0, hInst, 0,
	)
	if hwnd == 0 {
		fetchers.LogLine(appDir, fmt.Sprintf("[overlay32] tip: CreateWindowExW failed: %v", err))
		return
	}
	tip.hwnd = hwnd
}

// tipWndProc — подсказка ничего не делает: клики по ней не нужны, фокус тоже.
func tipWndProc(hwnd uintptr, msg uint32, wparam, lparam uintptr) uintptr {
	r, _, _ := procDefWindowProcW.Call(hwnd, uintptr(msg), wparam, lparam)
	return r
}

// show рисует подсказку над полосой s и показывает её.
func (t *hudTip) show(s *taskbarStrip, sum trayui.Summary) {
	if t.hwnd == 0 || !stripOn {
		return
	}
	lines := tipLines(sum)
	if len(lines) == 0 {
		return
	}
	key := tipKey(lines)
	if key != t.key || t.line == nil {
		if err := t.render(lines, s); err != nil {
			fetchers.LogLine(appDir, fmt.Sprintf("[overlay32] tip: render failed: %v", err))
			return
		}
		t.key = key
	}

	var barRect, stripRect RECT
	procGetWindowRect.Call(s.bar, uintptr(unsafe.Pointer(&barRect)))
	procGetWindowRect.Call(s.hwnd, uintptr(unsafe.Pointer(&stripRect)))

	// Прижимаем к правому краю полосы и поднимаем над панелью задач; если
	// сверху не влезает (панель сверху экрана) — показываем ниже.
	x := stripRect.Right - t.width
	y := barRect.Top - t.height - tipOffset
	if y < barRect.Top-4 && barRect.Top < 8 {
		y = barRect.Bottom + tipOffset
	}
	if x < 0 {
		x = 0
	}
	procSetWindowPos.Call(t.hwnd, HWND_TOP, uintptr(x), uintptr(y),
		uintptr(t.width), uintptr(t.height), uintptr(SWP_NOACTIVATE|SWP_SHOWWINDOW))
	if t.line != nil {
		t.line.presentChild(t.hwnd)
	}
}

// stop уничтожает окно подсказки при выходе.
func (t *hudTip) stop() {
	if t.line != nil {
		t.line.destroy()
		t.line = nil
	}
	if t.hwnd != 0 {
		procDestroyWindow.Call(t.hwnd)
		t.hwnd = 0
	}
	t.key = ""
}

// hide прячет подсказку (и запоминает, что её нет).
func (t *hudTip) hide() {
	if t.hwnd == 0 {
		return
	}
	procShowWindow.Call(t.hwnd, SW_HIDE)
}

// render пересобирает буфер под текущий набор строк.
//
// Подсказка рисуется таблицей: имя провайдера, затем по колонке на каждое
// окно — процент и остаток до сброса. Колонки считаются по самой широкой
// ячейке, поэтому «3д 5ч» не слипается со следующим «5ч 6%».
func (t *hudTip) render(lines []tipLine, s *taskbarStrip) error {
	font := clampF(float32(s.height)*11.5/48.0, 10, 18)
	lineH := font + tipLineGap

	probe, err := newLineRenderer(4096, int32(lineH)+tipPadY*2, font)
	if err != nil {
		return err
	}

	// Колонка иконок появляется, если иконка есть хотя бы у одной строки.
	iconSize := iconSizeFor(font)
	hasIcons := false
	for _, ln := range lines {
		if ln.Icon != "" {
			hasIcons = true
		}
	}
	iconColW := 0.0
	if hasIcons {
		iconColW = float64(iconSize) + float64(iconTextGap)
	}

	var nameW float64
	cols := 0
	for _, ln := range lines {
		if w := float64(probe.measure(ln.Name, true)); w > nameW {
			nameW = w
		}
		if len(ln.Cells) > cols {
			cols = len(ln.Cells)
		}
	}
	mainW := make([]float64, cols)
	resetW := make([]float64, cols)
	for _, ln := range lines {
		for i, cell := range ln.Cells {
			if w := float64(probe.measure(cell.Main, false)); w > mainW[i] {
				mainW[i] = w
			}
			if w := float64(probe.measure(cell.Reset, false)); w > resetW[i] {
				resetW[i] = w
			}
		}
	}
	probe.destroy()

	const (
		nameColGap = 12.0 // между именем и первой колонкой
		mainColGap = 8.0  // между процентом и остатком в ячейке
		cellGap    = 16.0 // между ячейками (окнами)
	)
	textW := float64(iconColW) + nameW + nameColGap
	for i := 0; i < cols; i++ {
		textW += mainW[i]
		if resetW[i] > 0 {
			textW += mainColGap + resetW[i]
		}
		if i < cols-1 {
			textW += cellGap
		}
	}
	width := int32(textW) + tipPadX*2
	height := int32(float32(len(lines))*lineH) + tipPadY*2
	if width < 40 {
		width = 40
	}

	if t.line != nil {
		t.line.destroy()
	}
	line, err := newLineRenderer(width, height, font)
	if err != nil {
		return err
	}
	t.line, t.width, t.height, t.font = line, width, height, font

	// Тень на 1 px и «матовая» плашка с тонкой рамкой — как у карточек HUD.
	line.fillRoundRect(0, 1, float32(width), float32(height), tipRadius, colorTipShadow, 0)
	line.fillRoundRect(0, 0, float32(width), float32(height)-1, tipRadius, colorTipFill, colorTipBorder)

	y := float32(tipPadY)
	for _, ln := range lines {
		x := float32(tipPadX)
		if iconColW > 0 {
			if ln.Icon != "" {
				if mask, err := trayui.LoadIcon(ln.Icon); err == nil {
					line.drawMask(mask.Scale(iconSize), int(math.Round(float64(x))),
						int(math.Round(float64(y+(lineH-float32(iconSize))/2))), tipInk)
				}
			}
			x += float32(iconColW)
		}
		line.drawTextAt(ln.Name, x, y, float32(nameW)+2, lineH, line.fontBold, colorTipName)
		x += float32(nameW + nameColGap)
		for i, cell := range ln.Cells {
			line.drawRun(cell.Main, x, y, lineH, line.font, colorTipValue)
			x += float32(mainW[i] + mainColGap)
			if cell.Reset != "" {
				line.drawRun(cell.Reset, x, y, lineH, line.font, colorTipReset)
			}
			x += float32(resetW[i] + cellGap)
		}
		y += lineH
	}
	return nil
}

// drawRun рисует кусок текста и возвращает его ширину, чтобы продолжать
// строку с точной позиции.
func (r *lineRenderer) drawRun(text string, x, y, h float32, font uintptr, color uint32) float32 {
	if text == "" {
		return 0
	}
	w := r.measure(text, font == r.fontBold)
	r.drawText(text, x, y, w+2, h, font, color)
	return w
}

// drawTextAt — строка в произвольной точке (в отличие от draw() полосы,
// здесь нужен многстрочный вывод и своя подсветка частей строки).
func (r *lineRenderer) drawTextAt(text string, x, y, w, h float32, font uintptr, color uint32) {
	r.drawText(text, x, y, w, h, font, color)
}

// fillRoundRect — плашка со скруглением (GDI+ path, как в Renderer).
func (r *lineRenderer) fillRoundRect(x, y, w, h, radius float32, fill, stroke uint32) {
	var path uintptr
	procGdipCreatePath.Call(0, uintptr(unsafe.Pointer(&path)))
	d := radius * 2
	procGdipAddPathArc.Call(path, fbits(x), fbits(y), fbits(d), fbits(d), fbits(180), fbits(90))
	procGdipAddPathArc.Call(path, fbits(x+w-d), fbits(y), fbits(d), fbits(d), fbits(270), fbits(90))
	procGdipAddPathArc.Call(path, fbits(x+w-d), fbits(y+h-d), fbits(d), fbits(d), fbits(0), fbits(90))
	procGdipAddPathArc.Call(path, fbits(x), fbits(y+h-d), fbits(d), fbits(d), fbits(90), fbits(90))
	procGdipClosePathFigure.Call(path)

	if (fill >> 24) > 0 {
		var brush uintptr
		procGdipCreateSolidFill.Call(uintptr(fill), uintptr(unsafe.Pointer(&brush)))
		procGdipFillPath.Call(r.graphics, brush, path)
		procGdipDeleteBrush.Call(brush)
	}
	if (stroke >> 24) > 0 {
		var pen uintptr
		procGdipCreatePen1.Call(uintptr(stroke), fbits(1.0), UnitPixel, uintptr(unsafe.Pointer(&pen)))
		procGdipDrawPath.Call(r.graphics, pen, path)
		procGdipDeletePen.Call(pen)
	}
	procGdipDeletePath.Call(path)
}

// tipLines — по строке на провайдера: имя в колонку, затем окна с процентом
// и остатком до сброса. Остаток берётся из reset_in и переводится в русские
// единицы тем же форматтером, что и карточки оверлея.
func tipLines(sum trayui.Summary) []tipLine {
	out := make([]tipLine, 0, len(sum.Items))
	for _, it := range sum.Items {
		ln := tipLine{Icon: it.Icon, Name: it.Label}
		for _, v := range it.Values {
			reset := formatResetTimeLabel(v.Reset)
			if reset == "--" {
				reset = ""
			}
			ln.Cells = append(ln.Cells, tipCell{Main: v.String(), Reset: reset})
		}
		if len(ln.Cells) == 0 {
			note := it.Note
			if note == "" {
				note = "нет данных"
			}
			ln.Cells = append(ln.Cells, tipCell{Main: note})
		}
		out = append(out, ln)
	}
	return out
}

// lineCellsText — все ячейки строки одной строкой (ключ «изменилось ли»).
func lineCellsText(ln tipLine) string {
	parts := make([]string, 0, len(ln.Cells))
	for _, c := range ln.Cells {
		parts = append(parts, c.text())
	}
	return strings.Join(parts, "|")
}

func tipKey(lines []tipLine) string {
	key := ""
	for _, ln := range lines {
		key += ln.Name + "\x00" + lineCellsText(ln) + "\x00"
	}
	return key
}

// tipHoverStart взводит таймер показа и подписку на WM_MOUSELEAVE.
func tipHoverStart(hwnd uintptr) {
	tme := TRACKMOUSEEVENT{
		CbSize:    uint32(unsafe.Sizeof(TRACKMOUSEEVENT{})),
		DwFlags:   TME_LEAVE,
		HwndTrack: hwnd,
	}
	procTrackMouseEvnt.Call(uintptr(unsafe.Pointer(&tme)))
	procKillTimer.Call(hwnd, 1)
	procSetTimer.Call(hwnd, 1, uintptr(tipHoverDelayMs), 0)
}

// tipHoverStop снимает таймер и прячет подсказку.
func tipHoverStop(hwnd uintptr) {
	procKillTimer.Call(hwnd, 1)
	tip.hide()
}
