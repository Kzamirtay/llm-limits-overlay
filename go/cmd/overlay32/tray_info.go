//go:build windows

package main

import (
	"fmt"
	"image"
	"unsafe"

	"golang.org/x/sys/windows/registry"

	"llmlimits/internal/fetchers"
	"llmlimits/internal/trayui"
)

const (
	// trayPercentStep — шаг, с которым пересобирается значок: HICON
	// пересоздаётся только при заметном изменении, иначе каждые 60 с мы бы
	// дёргали оболочку без причины. На 16 px разница в 5% не видна —
	// точные числа живут в подсказке и меню.
	trayPercentStep = 5.0

	// Ключ реестра, по которому Windows сообщает, светлая ли сейчас тема
	// оформления: значки в трее не инвертируются автоматически, и белое
	// кольцо на светлой панели задач просто исчезает.
	personalizeKey  = `Software\Microsoft\Windows\CurrentVersion\Themes\Personalize`
	lightThemeValue = "SystemUsesLightTheme"

	// SM_CXSMICON — размер значка в области уведомлений при текущем DPI
	// (16 px при 100%, 20 при 125%, 24 при 150%).
	SM_CXSMICON = 49
)

// trayIconKeyString — состояние, из которого собран значок. Пока ключ не
// изменился, HICON не пересобирается.
func trayIconKeyString(it trayui.Item) string {
	pct := trayui.QuantizePercent(it.Worst, trayPercentStep)
	return fmt.Sprintf("%t|%d|%s", taskbarIsLight(), int(pct), it.Glyph)
}

// buildTrayHIcon рисует значок провайдера и превращает пиксели в HICON.
// Размер берётся из системной метрики, а не из набора 16/24/32: оболочка
// показывает значок именно в SM_CXSMICON, и рисовать «под масштабирование»
// значит получить мыло на любом DPI кроме 100%.
func buildTrayHIcon(it trayui.Item) uintptr {
	r, _, _ := procGetSystemMetrics.Call(SM_CXSMICON)
	size := int(r)
	if size <= 0 || size > 64 {
		size = 16
	}
	state := it.State
	fraction := it.Worst / 100.0
	if state != trayui.StateOK {
		fraction = 0
	}
	img := trayui.Badge(size, fraction, state, paletteForTaskbar(taskbarIsLight()), it.Glyph)
	return makeHIcon(img)
}

// paletteForTaskbar подбирает цвета кольца под текущую тему панели задач.
// Значок монохромный (как и весь оверлей): на тёмной панели светлое кольцо,
// на светлой — тёмное, трек всегда приглушённый.
func paletteForTaskbar(light bool) trayui.Palette {
	if !light {
		return trayui.Palette{
			Track: trayui.RGB{R: 0xF2, G: 0xF2, B: 0xF2}, TrackA: 64,
			Arc: trayui.RGB{R: 0xF7, G: 0xF7, B: 0xF7}, ArcA: 235,
		}
	}
	return trayui.Palette{
		Track: trayui.RGB{R: 0x20, G: 0x20, B: 0x20}, TrackA: 70,
		Arc: trayui.RGB{R: 0x1A, G: 0x1A, B: 0x1A}, ArcA: 240,
	}
}

// taskbarIsLight читает HKCU: SystemUsesLightTheme = 1 → светлая панель задач.
// Ошибка чтения трактуется как тёмная тема (дефолт Windows 11).
func taskbarIsLight() bool {
	k, err := registry.OpenKey(registry.CURRENT_USER, personalizeKey, registry.QUERY_VALUE)
	if err != nil {
		return false
	}
	defer k.Close()
	v, _, err := k.GetIntegerValue(lightThemeValue)
	if err != nil {
		return false
	}
	return v == 1
}

// makeHIcon собирает HICON прямо из пикселей: цветной DIB 32bpp (BGRA,
// прямая альфа) + монохромная AND-маска. Без файлов в %TEMP% — в отличие от
// fyne.io/systray, который на каждый образ писал отдельный файл.
func makeHIcon(img *image.NRGBA) uintptr {
	w, h := img.Rect.Dx(), img.Rect.Dy()
	if w <= 0 || h <= 0 {
		return 0
	}

	bmi := BITMAPINFOHEADER{
		BiSize:        uint32(unsafe.Sizeof(BITMAPINFOHEADER{})),
		BiWidth:       int32(w),
		BiHeight:      int32(-h), // top-down: строки сверху вниз
		BiPlanes:      1,
		BiBitCount:    32,
		BiCompression: 0, // BI_RGB
	}
	var bits unsafe.Pointer
	hbm, _, _ := procCreateDIBSection.Call(
		0,
		uintptr(unsafe.Pointer(&bmi)),
		0, // DIB_RGB_COLORS
		uintptr(unsafe.Pointer(&bits)),
		0, 0,
	)
	if hbm == 0 || bits == nil {
		return 0
	}

	dst := unsafe.Slice((*byte)(bits), w*h*4)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			s := y*img.Stride + x*4
			d := (y*w + x) * 4
			dst[d+0] = img.Pix[s+2] // B
			dst[d+1] = img.Pix[s+1] // G
			dst[d+2] = img.Pix[s+0] // R
			dst[d+3] = img.Pix[s+3] // A
		}
	}

	maskStride := ((w + 31) / 32) * 4
	mask := make([]byte, maskStride*h)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			if img.Pix[y*img.Stride+x*4+3] == 0 {
				mask[y*maskStride+x/8] |= 1 << (7 - uint(x%8))
			}
		}
	}
	hbmMask, _, _ := procCreateBitmap.Call(
		uintptr(w), uintptr(h), 1, 1,
		uintptr(unsafe.Pointer(&mask[0])),
	)

	ii := ICONINFO{FIcon: 1, HbmMask: hbmMask, HbmColor: hbm}
	hicon, _, _ := procCreateIconInd.Call(uintptr(unsafe.Pointer(&ii)))

	// CreateIconIndirect копирует битмапы, исходники освобождаем сразу.
	if hbmMask != 0 {
		procDeleteObject.Call(hbmMask)
	}
	procDeleteObject.Call(hbm)

	if hicon == 0 {
		fetchers.LogLine(appDir, fmt.Sprintf("[overlay32] CreateIconIndirect failed (%dx%d)", w, h))
	}
	return hicon
}
