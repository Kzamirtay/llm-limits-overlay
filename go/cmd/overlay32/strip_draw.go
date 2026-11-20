//go:build windows

package main

import (
	"fmt"
	"math"
	"unsafe"

	"golang.org/x/sys/windows"

	"llmlimits/internal/trayui"
)

// StripStringFlags/Align — однострочная строка без переносов, выровненная
// по вертикали центра полосы. Без NoWrap GDI+ может перенести «O 19%» на
// вторую строку, если ширина окажется на пиксель меньше измеренной.
const (
	stringFormatFlagsNoWrap  = 0x00001000
	stringFormatFlagsNoClip  = 0x00004000
	stringFormatLineAlignMid = 1 // StringAlignmentCenter
	stringFormatAlignNear    = 0
)

var procGdipSetStringFormatFlags = gdiplus.NewProc("GdipSetStringFormatFlags")

// lineRenderer — минимальный аналог Renderer для одной строки текста:
// тот же DIB 32bpp + GDI+, но один размер шрифта и два начертания.
// Отдельный тип, а не Renderer: у него карточки, четыре шрифта и своя
// геометрия, и тащить всё это в полосу на панели задач незачем.
type lineRenderer struct {
	width, height int32
	// fontSize — кегль в пикселях (font/fontBold — это хэндлы GDI+, по ним
	// размер не восстановить).
	fontSize float32
	memDC    uintptr
	hbmp     uintptr
	gpBitmap uintptr
	graphics uintptr
	bits     unsafe.Pointer
	family   uintptr
	font     uintptr
	fontBold uintptr
	fmt      uintptr
}

func newLineRenderer(width, height int32, fontSize float32) (*lineRenderer, error) {
	memDC, _, err := procCreateCompatibleDC.Call(0)
	if memDC == 0 {
		return nil, fmt.Errorf("CreateCompatibleDC failed: %v", err)
	}
	bmi := BITMAPINFOHEADER{
		BiSize:        uint32(unsafe.Sizeof(BITMAPINFOHEADER{})),
		BiWidth:       width,
		BiHeight:      -height, // top-down
		BiPlanes:      1,
		BiBitCount:    32,
		BiCompression: 0,
	}
	var bits unsafe.Pointer
	hbmp, _, err := procCreateDIBSection.Call(memDC, uintptr(unsafe.Pointer(&bmi)), 0,
		uintptr(unsafe.Pointer(&bits)), 0, 0)
	if hbmp == 0 {
		procDeleteDC.Call(memDC)
		return nil, fmt.Errorf("CreateDIBSection failed: %v", err)
	}
	procSelectObject.Call(memDC, hbmp)

	var gpBitmap uintptr
	if r, _, _ := procGdipCreateBitmapFromScan0.Call(
		uintptr(width), uintptr(height), uintptr(width*4),
		PixelFormat32bppPARGB, uintptr(bits),
		uintptr(unsafe.Pointer(&gpBitmap)),
	); r != 0 {
		procDeleteObject.Call(hbmp)
		procDeleteDC.Call(memDC)
		return nil, fmt.Errorf("GdipCreateBitmapFromScan0 failed: %d", r)
	}

	var g uintptr
	procGdipGetImageGraphicsContext.Call(gpBitmap, uintptr(unsafe.Pointer(&g)))
	procGdipSetSmoothingMode.Call(g, SmoothingModeAntiAlias)
	procGdipSetTextRenderingHint.Call(g, TextRenderingHintAntiAliasGridFit)

	family := createFontFamily()

	var font, fontBold uintptr
	procGdipCreateFont.Call(family, fbits(fontSize), 0, UnitPixel, uintptr(unsafe.Pointer(&font)))
	procGdipCreateFont.Call(family, fbits(fontSize), 1, UnitPixel, uintptr(unsafe.Pointer(&fontBold)))

	var format uintptr
	procGdipCreateStringFormat.Call(0, 0, uintptr(unsafe.Pointer(&format)))
	procGdipSetStringFormatAlign.Call(format, stringFormatAlignNear)
	procGdipSetStringFormatFlags.Call(format, stringFormatFlagsNoWrap|stringFormatFlagsNoClip)
	procGdipSetStringFormatLineAlign.Call(format, stringFormatLineAlignMid)

	return &lineRenderer{
		width: width, height: height, fontSize: fontSize,
		memDC: memDC, hbmp: hbmp, bits: bits, gpBitmap: gpBitmap, graphics: g,
		family: family, font: font, fontBold: fontBold, fmt: format,
	}, nil
}

func (r *lineRenderer) destroy() {
	if r == nil {
		return
	}
	for _, h := range []uintptr{r.fmt, r.font, r.fontBold, r.family} {
		if h == 0 {
			continue
		}
	}
	if r.fmt != 0 {
		procGdipDeleteStringFormat.Call(r.fmt)
	}
	if r.font != 0 {
		procGdipDeleteFont.Call(r.font)
	}
	if r.fontBold != 0 {
		procGdipDeleteFont.Call(r.fontBold)
	}
	if r.family != 0 {
		procGdipDeleteFontFamily.Call(r.family)
	}
	if r.graphics != 0 {
		procGdipDeleteGraphics.Call(r.graphics)
	}
	if r.gpBitmap != 0 {
		procGdipDisposeImage.Call(r.gpBitmap)
	}
	if r.hbmp != 0 {
		procDeleteObject.Call(r.hbmp)
	}
	if r.memDC != 0 {
		procDeleteDC.Call(r.memDC)
	}
}

// textGamma — таблица «яркость покрытия → альфа» с гаммой 0.65.
var textGamma = func() [256]uint8 {
	var t [256]uint8
	for i := 0; i < 256; i++ {
		v := math.Pow(float64(i)/255, 0.65) * 255
		if v < 1 && i > 0 {
			v = 1
		}
		t[i] = uint8(math.Round(v))
	}
	return t
}()

// createFontFamily берёт первый доступный системный шрифт из списка.
// Windows 11 рисует панель задач Segoe UI Variable; на Windows 10 его нет,
// поэтому пробуем по порядку и падаем на обычный Segoe UI.
func createFontFamily() uintptr {
	candidates := []string{
		// Полужирный: у системного текста панели задач штрихи плотнее, чем
		// даёт Regular с монохромным сглаживанием (ClearType по непрозрачному
		// фону нам недоступен — окно слоистое и прозрачное).
		"Segoe UI Variable Text Semibold",
		"Segoe UI Semibold",
		"Segoe UI",
	}
	for _, name := range candidates {
		ptr, err := windows.UTF16PtrFromString(name)
		if err != nil {
			continue
		}
		var family uintptr
		if r, _, _ := procGdipCreateFontFamilyFromName.Call(
			uintptr(unsafe.Pointer(ptr)), 0, uintptr(unsafe.Pointer(&family)),
		); r == 0 && family != 0 {
			return family
		}
	}
	var family uintptr
	procGdipCreateFontFamilyFromName.Call(0, 0, uintptr(unsafe.Pointer(&family)))
	return family
}

// measure — ширина строки в пикселях для выбранного начертания.
func (r *lineRenderer) measure(text string, bold bool) float32 {
	font := r.font
	if bold {
		font = r.fontBold
	}
	strPtr, err := windows.UTF16PtrFromString(text)
	if err != nil {
		return 0
	}
	rect := RectF{X: 0, Y: 0, Width: float32(r.width), Height: float32(r.height)}
	var bbox RectF
	procGdipMeasureString.Call(
		r.graphics,
		uintptr(unsafe.Pointer(strPtr)),
		uintptr(^uintptr(0)),
		font,
		uintptr(unsafe.Pointer(&rect)),
		r.fmt,
		uintptr(unsafe.Pointer(&bbox)),
		0, 0,
	)
	return bbox.Width
}

// draw рисует строку так, чтобы штрихи не «худели».
//
// Приём: текст рисуется белым по НЕПРОЗРАЧНОМУ чёрному буферу, а затем
// яркость каждого пикселя превращается в альфу нужного цвета. Если писать
// GDI+ прямо на прозрачный фон, антиалиасинг размазывает штрих на два пикселя
// по ~60% альфы, и на панели задач текст выглядит серым и тонким рядом с
// системным (там ClearType по непрозрачному фону). Здесь штрих получает
// полную альфу и нужный цвет — при сохранённом сглаживании.
func (r *lineRenderer) draw(segs []trayui.InlineSegment, ink trayui.RGB, padX float32) {
	if r.bits == nil {
		return
	}
	n := int(r.width * r.height * 4)
	px := unsafe.Slice((*byte)(r.bits), n)
	// Непрозрачный чёрный фон (BGRA).
	for i := 0; i < n; i += 4 {
		px[i], px[i+1], px[i+2], px[i+3] = 0, 0, 0, 255
	}

	// Размер иконки — по кеглю шрифта: глиф не должен быть выше строчных.
	iconSize := iconSizeFor(r.fontSize)
	iconY := (int(r.height) - iconSize) / 2

	x := padX
	for _, seg := range segs {
		// InlineDim — приглушённые данные: рисуем серым, он же станет альфой.
		level := uint8(255)
		if seg.Style == trayui.InlineDim {
			level = 150
		}
		ink := trayui.RGB{R: level, G: level, B: level}

		if seg.Icon != "" {
			if mask, err := trayui.LoadIcon(seg.Icon); err == nil {
				r.drawMask(mask.Scale(iconSize), int(math.Round(float64(x))), iconY, ink)
				x += float32(iconSize) + iconTextGap
			}
		}
		bold := seg.Style == trayui.InlineBold
		font := r.font
		if bold {
			font = r.fontBold
		}
		r.drawText(seg.Text, x, 0, float32(r.width), float32(r.height), font,
			argb(255, level, level, level))
		x += r.measure(seg.Text, bold)
	}

	// Яркость → альфа и премультиплицированный цвет текста. Гамма < 1
	// поднимает непрозрачность полутонов: монохромное сглаживание при 12 px
	// даёт штрих ~60% покрытия, и рядом с системным текстом он выглядит
	// бледным. ClearType по непрозрачному фону делает ту же работу гаммой.
	// Минимальная
	// альфа 1 оставляет окно «ловящим» мышь: у слоистого окна хит-тест
	// считается по альфе, и при нуле клики уходили бы в панель задач.
	for i := 0; i < n; i += 4 {
		lum := px[i]
		if px[i+1] > lum {
			lum = px[i+1]
		}
		if px[i+2] > lum {
			lum = px[i+2]
		}
		if lum == 0 {
			px[i], px[i+1], px[i+2], px[i+3] = 0, 0, 0, 1
			continue
		}
		a := textGamma[lum]
		px[i] = uint8(uint32(ink.B) * uint32(a) / 255)
		px[i+1] = uint8(uint32(ink.G) * uint32(a) / 255)
		px[i+2] = uint8(uint32(ink.R) * uint32(a) / 255)
		px[i+3] = a
	}
}

// iconTextGap — зазор между иконкой провайдера и его процентом.
const iconTextGap = 4

// iconSizeFor — размер глифа провайдера под кегль текста. Одна функция на
// отрисовку и на расчёт ширины полосы: иначе окно окажется уже содержимого и
// последний провайдер обрежется.
func iconSizeFor(fontSize float32) int {
	size := int(math.Round(float64(fontSize) * 1.15))
	if size < 8 {
		size = 8
	}
	return size
}

// iconRunWidth — сколько места занимает иконка вместе с зазором.
func iconRunWidth(fontSize float32) float32 {
	return float32(iconSizeFor(fontSize)) + iconTextGap
}

// drawMask накладывает монохромную маску (source-over в премультиплицированном
// пространстве): так иконка провайдера тонируется в цвет текста и одинаково
// работает и в полосе (белым по чёрному, яркость потом станет альфой), и на
// светлой плашке подсказки.
func (r *lineRenderer) drawMask(m *trayui.IconMask, x, y int, ink trayui.RGB) {
	if m == nil || m.Size <= 0 || r.bits == nil {
		return
	}
	px := unsafe.Slice((*byte)(r.bits), int(r.width*r.height*4))
	for my := 0; my < m.Size; my++ {
		py := y + my
		if py < 0 || py >= int(r.height) {
			continue
		}
		for mx := 0; mx < m.Size; mx++ {
			pxx := x + mx
			if pxx < 0 || pxx >= int(r.width) {
				continue
			}
			a := float64(m.Alpha[my*m.Size+mx]) / 255
			if a <= 0 {
				continue
			}
			i := (py*int(r.width) + pxx) * 4
			inv := 1 - a
			b := float64(ink.B)*a + float64(px[i])*inv
			g := float64(ink.G)*a + float64(px[i+1])*inv
			rr := float64(ink.R)*a + float64(px[i+2])*inv
			al := a*255 + float64(px[i+3])*inv
			px[i], px[i+1], px[i+2], px[i+3] = clampByte(b), clampByte(g), clampByte(rr), clampByte(al)
		}
	}
}

func clampByte(v float64) uint8 {
	if v <= 0 {
		return 0
	}
	if v >= 255 {
		return 255
	}
	return uint8(math.Round(v))
}

func (r *lineRenderer) drawText(text string, x, y, w, h float32, font uintptr, color uint32) {
	strPtr, err := windows.UTF16PtrFromString(text)
	if err != nil {
		return
	}
	var brush uintptr
	procGdipCreateSolidFill.Call(uintptr(color), uintptr(unsafe.Pointer(&brush)))
	rect := RectF{X: x, Y: y, Width: w, Height: h}
	procGdipDrawString.Call(
		r.graphics,
		uintptr(unsafe.Pointer(strPtr)),
		uintptr(^uintptr(0)),
		font,
		uintptr(unsafe.Pointer(&rect)),
		r.fmt,
		brush,
	)
	procGdipDeleteBrush.Call(brush)
}

// presentChild выводит буфер в дочернее слоистое окно. В отличие от
// Renderer.Present здесь pptDst = NULL: позицию дочернего окна задаёт
// SetWindowPos родителя, а не UpdateLayeredWindow.
func (r *lineRenderer) presentChild(hwnd uintptr) error {
	sz := SIZE{CX: r.width, CY: r.height}
	ptSrc := POINT{X: 0, Y: 0}
	blend := BLENDFUNCTION{
		BlendOp:             AC_SRC_OVER,
		BlendFlags:          0,
		SourceConstantAlpha: 255,
		AlphaFormat:         AC_SRC_ALPHA,
	}
	ret, _, err := procUpdateLayeredWindow.Call(
		hwnd,
		0,
		0,
		uintptr(unsafe.Pointer(&sz)),
		r.memDC,
		uintptr(unsafe.Pointer(&ptSrc)),
		0,
		uintptr(unsafe.Pointer(&blend)),
		ULW_ALPHA,
	)
	if ret == 0 {
		return fmt.Errorf("UpdateLayeredWindow(strip) failed: %v", err)
	}
	return nil
}
