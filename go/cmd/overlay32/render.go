package main

import (
	"fmt"
	"math"
	"strings"
	"time"
	"unsafe"

	"llmlimits/internal/config"
	"llmlimits/internal/model"

	"golang.org/x/sys/windows"
)

// Constants from OverlayTheme.cs
const (
	podCornerRadius = float32(8.0)
	barHeight       = float32(5.5)
	barCornerRadius = float32(2.5)
)

var (
	// ~49% translucent frosted slate: Color.FromArgb(0x7C, 0xEE, 0xF2, 0xF6)
	colorPodBg = argb(124, 238, 242, 246)
	// ~12% subtle border: Color.FromArgb(0x20, 0x64, 0x74, 0x8B)
	colorPodBorder = argb(32, 100, 116, 139)
	// Subtle pod shadow
	colorPodShadow = argb(15, 0, 0, 0)

	// Headers: TitleTextColor, GeminiHeaderColor, ClaudeHeaderColor, OpenRouterHeaderColor, KimiHeaderColor = Colors.Black
	colorHeader = argb(255, 0, 0, 0)

	// LabelTextColor, PercentTextColor, MetaTextColor = #0F172A
	colorLabel = argb(255, 15, 23, 42)
	// PercentText100Color = Colors.Black
	colorPercent100 = argb(255, 0, 0, 0)

	// MetaTextMutedColor, UpdatedStaleTextColor = #475569
	colorMetaStale = argb(255, 71, 85, 105)

	// BarTrackBackgroundColor = Color.FromArgb(0x22, 0x00, 0x00, 0x00) (~13.3% alpha black)
	colorBarTrack = argb(34, 0, 0, 0)

	// DividerBrush = Color.FromArgb(0x18, 0, 0, 0)
	colorDivider = argb(24, 0, 0, 0)
)

type Renderer struct {
	width  int32
	height int32

	memDC    uintptr
	hbmp     uintptr
	bits     unsafe.Pointer
	gpBitmap uintptr
	graphics uintptr

	family      uintptr
	fontTitle   uintptr // 11px Bold
	fontRow     uintptr // 11.5px Regular
	fontRowBold uintptr // 11.5px Bold
	fontMeta    uintptr // 10px Regular
	fmtNear     uintptr
	fmtFar      uintptr
}

func NewRenderer(w, h int32) (*Renderer, error) {
	memDC, _, err := procCreateCompatibleDC.Call(0)
	if memDC == 0 {
		return nil, fmt.Errorf("CreateCompatibleDC failed: %v", err)
	}

	bmi := BITMAPINFOHEADER{
		BiSize:        uint32(unsafe.Sizeof(BITMAPINFOHEADER{})),
		BiWidth:       w,
		BiHeight:      -h, // top-down DIB
		BiPlanes:      1,
		BiBitCount:    32,
		BiCompression: 0,
	}

	var bits unsafe.Pointer
	hbmp, _, err := procCreateDIBSection.Call(
		memDC,
		uintptr(unsafe.Pointer(&bmi)),
		0,
		uintptr(unsafe.Pointer(&bits)),
		0, 0,
	)
	if hbmp == 0 {
		procDeleteDC.Call(memDC)
		return nil, fmt.Errorf("CreateDIBSection failed: %v", err)
	}
	procSelectObject.Call(memDC, hbmp)

	var gpBitmap uintptr
	r, _, _ := procGdipCreateBitmapFromScan0.Call(
		uintptr(w),
		uintptr(h),
		uintptr(w*4),
		PixelFormat32bppPARGB,
		uintptr(bits),
		uintptr(unsafe.Pointer(&gpBitmap)),
	)
	if r != 0 {
		procDeleteObject.Call(hbmp)
		procDeleteDC.Call(memDC)
		return nil, fmt.Errorf("GdipCreateBitmapFromScan0 failed with code: %d", r)
	}

	var g uintptr
	procGdipGetImageGraphicsContext.Call(gpBitmap, uintptr(unsafe.Pointer(&g)))
	procGdipSetSmoothingMode.Call(g, SmoothingModeAntiAlias)
	procGdipSetTextRenderingHint.Call(g, TextRenderingHintClearType)

	// Fonts: Segoe UI
	var family uintptr
	fontName, _ := windows.UTF16PtrFromString("Segoe UI")
	procGdipCreateFontFamilyFromName.Call(uintptr(unsafe.Pointer(fontName)), 0, uintptr(unsafe.Pointer(&family)))

	var fontTitle, fontRow, fontRowBold, fontMeta uintptr
	procGdipCreateFont.Call(family, fbits(11.0), 1, UnitPixel, uintptr(unsafe.Pointer(&fontTitle)))   // 11px Bold
	procGdipCreateFont.Call(family, fbits(11.5), 0, UnitPixel, uintptr(unsafe.Pointer(&fontRow)))     // 11.5px Regular
	procGdipCreateFont.Call(family, fbits(11.5), 1, UnitPixel, uintptr(unsafe.Pointer(&fontRowBold))) // 11.5px Bold
	procGdipCreateFont.Call(family, fbits(10.0), 0, UnitPixel, uintptr(unsafe.Pointer(&fontMeta)))    // 10px Regular

	var fmtNear, fmtFar uintptr
	procGdipCreateStringFormat.Call(0, 0, uintptr(unsafe.Pointer(&fmtNear)))
	procGdipCreateStringFormat.Call(0, 0, uintptr(unsafe.Pointer(&fmtFar)))
	procGdipSetStringFormatAlign.Call(fmtFar, 2) // Far = Right

	return &Renderer{
		width:       w,
		height:      h,
		memDC:       memDC,
		hbmp:        hbmp,
		bits:        bits,
		gpBitmap:    gpBitmap,
		graphics:    g,
		family:      family,
		fontTitle:   fontTitle,
		fontRow:     fontRow,
		fontRowBold: fontRowBold,
		fontMeta:    fontMeta,
		fmtNear:     fmtNear,
		fmtFar:      fmtFar,
	}, nil
}

func (r *Renderer) Destroy() {
	if r.fmtNear != 0 {
		procGdipDeleteStringFormat.Call(r.fmtNear)
	}
	if r.fmtFar != 0 {
		procGdipDeleteStringFormat.Call(r.fmtFar)
	}
	if r.fontTitle != 0 {
		procGdipDeleteFont.Call(r.fontTitle)
	}
	if r.fontRow != 0 {
		procGdipDeleteFont.Call(r.fontRow)
	}
	if r.fontRowBold != 0 {
		procGdipDeleteFont.Call(r.fontRowBold)
	}
	if r.fontMeta != 0 {
		procGdipDeleteFont.Call(r.fontMeta)
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

// Present обновляет окно на экране через UpdateLayeredWindow
func (r *Renderer) Present(hwnd uintptr, x, y int32) error {
	ptDst := POINT{X: x, Y: y}
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
		uintptr(unsafe.Pointer(&ptDst)),
		uintptr(unsafe.Pointer(&sz)),
		r.memDC,
		uintptr(unsafe.Pointer(&ptSrc)),
		0,
		uintptr(unsafe.Pointer(&blend)),
		ULW_ALPHA,
	)
	if ret == 0 {
		return fmt.Errorf("UpdateLayeredWindow failed: %v", err)
	}
	return nil
}

func (r *Renderer) drawRoundedRect(x, y, w, h, radius float32, fillArgb, strokeArgb uint32) {
	var path uintptr
	procGdipCreatePath.Call(0, uintptr(unsafe.Pointer(&path)))
	d := radius * 2
	procGdipAddPathArc.Call(path, fbits(x), fbits(y), fbits(d), fbits(d), fbits(180), fbits(90))
	procGdipAddPathArc.Call(path, fbits(x+w-d), fbits(y), fbits(d), fbits(d), fbits(270), fbits(90))
	procGdipAddPathArc.Call(path, fbits(x+w-d), fbits(y+h-d), fbits(d), fbits(d), fbits(0), fbits(90))
	procGdipAddPathArc.Call(path, fbits(x), fbits(y+h-d), fbits(d), fbits(d), fbits(90), fbits(90))
	procGdipClosePathFigure.Call(path)

	if (fillArgb >> 24) > 0 {
		var brush uintptr
		procGdipCreateSolidFill.Call(uintptr(fillArgb), uintptr(unsafe.Pointer(&brush)))
		procGdipFillPath.Call(r.graphics, brush, path)
		procGdipDeleteBrush.Call(brush)
	}

	if (strokeArgb >> 24) > 0 {
		var pen uintptr
		procGdipCreatePen1.Call(uintptr(strokeArgb), fbits(1.0), UnitPixel, uintptr(unsafe.Pointer(&pen)))
		procGdipDrawPath.Call(r.graphics, pen, path)
		procGdipDeletePen.Call(pen)
	}

	procGdipDeletePath.Call(path)
}

func (r *Renderer) drawCardBackground(x, y, w, h float32) {
	// Subtle drop shadow shifted by 1px
	r.drawRoundedRect(x, y+1, w, h, podCornerRadius, colorPodShadow, 0)
	// Pod frosted slate background + border
	r.drawRoundedRect(x, y, w, h, podCornerRadius, colorPodBg, colorPodBorder)
}

// getProgressColor — линейная интерполяция между #4B5563 (used=0.0) и #111827 (used=1.0)
// Полностью соответствует GetProgressBrush в OverlayTheme.cs
func getProgressColor(usedFraction float64) uint32 {
	if math.IsNaN(usedFraction) || usedFraction < 0.0 {
		usedFraction = 0.0
	}
	if usedFraction > 1.0 {
		usedFraction = 1.0
	}
	r1, g1, b1 := 75.0, 85.0, 99.0
	r2, g2, b2 := 17.0, 24.0, 39.0
	r := byte(math.Round(r1 + (r2-r1)*usedFraction))
	g := byte(math.Round(g1 + (g2-g1)*usedFraction))
	b := byte(math.Round(b1 + (b2-b1)*usedFraction))
	return argb(255, r, g, b)
}

func (r *Renderer) drawProgressBar(x, y, w, h float32, usedFraction float64) {
	// Трек бара: rgba(0, 0, 0, 0.133)
	r.drawRoundedRect(x, y, w, h, barCornerRadius, colorBarTrack, 0)

	if math.IsNaN(usedFraction) || usedFraction <= 0.0 {
		return
	}
	if usedFraction > 1.0 {
		usedFraction = 1.0
	}
	fillW := float32(float64(w) * usedFraction)
	if fillW < h {
		fillW = h
	}
	fillColor := getProgressColor(usedFraction)
	r.drawRoundedRect(x, y, fillW, h, barCornerRadius, fillColor, 0)
}

func (r *Renderer) drawString(text string, x, y, w, h float32, font uintptr, color uint32, alignFar bool) {
	strPtr, _ := windows.UTF16PtrFromString(text)
	var brush uintptr
	procGdipCreateSolidFill.Call(uintptr(color), uintptr(unsafe.Pointer(&brush)))
	rect := RectF{X: x, Y: y, Width: w, Height: h}
	fmt := r.fmtNear
	if alignFar {
		fmt = r.fmtFar
	}
	procGdipDrawString.Call(
		r.graphics,
		uintptr(unsafe.Pointer(strPtr)),
		uintptr(^uintptr(0)),
		font,
		uintptr(unsafe.Pointer(&rect)),
		fmt,
		brush,
	)
	procGdipDeleteBrush.Call(brush)
}

func (r *Renderer) measureStringWidth(text string, font uintptr) float32 {
	strPtr, _ := windows.UTF16PtrFromString(text)
	rect := RectF{X: 0, Y: 0, Width: 1000, Height: 50}
	var bbox RectF
	procGdipMeasureString.Call(
		r.graphics,
		uintptr(unsafe.Pointer(strPtr)),
		uintptr(^uintptr(0)),
		font,
		uintptr(unsafe.Pointer(&rect)),
		r.fmtNear,
		uintptr(unsafe.Pointer(&bbox)),
		0, 0,
	)
	return bbox.Width
}

// formatResetTimeLabel — 1:1 аналог FormatResetTimeLabel из OverlayTheme.cs
func formatResetTimeLabel(resetIn string) string {
	if resetIn == "" || resetIn == "--" || resetIn == "N/A" {
		return "--"
	}
	if resetIn == "ready" {
		return "готов"
	}
	ru := strings.ReplaceAll(resetIn, "d", "д ")
	ru = strings.ReplaceAll(ru, "h", "ч ")
	ru = strings.ReplaceAll(ru, "m", "м")
	return strings.TrimSpace(ru)
}

// getRelativeTimeString — 1:1 аналог GetRelativeTimeText и UpdateCardUpdatedLine из C#
func getRelativeTimeString(updatedIso, status string) (string, uint32) {
	if status == "no_key" {
		return "нет ключа", colorLabel
	}
	isStale := (status == "stale")
	if updatedIso == "" {
		if isStale {
			return "устарело", colorMetaStale
		}
		return "нет данных", colorLabel
	}

	formats := []string{
		"2006-01-02 15:04:05",
		"2006-01-02T15:04:05",
		time.RFC3339,
		"2006/01/02 15:04:05",
	}
	var t time.Time
	var err error
	for _, f := range formats {
		t, err = time.Parse(f, updatedIso)
		if err == nil {
			break
		}
	}
	if err != nil {
		if isStale {
			return "устарело", colorMetaStale
		}
		return "обновлено", colorLabel
	}

	diff := time.Since(t)
	totalMinutes := int(math.Floor(diff.Minutes()))
	if totalMinutes < 0 {
		totalMinutes = 0
	}

	var baseText string
	if totalMinutes < 1 {
		baseText = "только что"
	} else if totalMinutes < 60 {
		baseText = fmt.Sprintf("%d мин", totalMinutes)
	} else {
		hrs := totalMinutes / 60
		mins := totalMinutes % 60
		if mins > 0 {
			baseText = fmt.Sprintf("%dч %dм", hrs, mins)
		} else {
			baseText = fmt.Sprintf("%dч", hrs)
		}
	}

	if isStale {
		return baseText + " (stale)", colorMetaStale
	}
	return baseText, colorLabel
}

// formatPercent — точный формат C#: b.Percent + "%"
func formatPercent(pct float64) string {
	if math.Mod(pct*10, 10) == 0 {
		return fmt.Sprintf("%.0f%%", pct)
	}
	return fmt.Sprintf("%.1f%%", pct)
}

func getPercentColorAndFont(pct, frac float64, r *Renderer) (uint32, uintptr) {
	if frac >= 1.0 || pct >= 100.0 {
		return colorPercent100, r.fontRowBold
	}
	return colorLabel, r.fontRow
}

func findProviderState(snap *model.Snapshot, id string) *model.ProviderState {
	if snap == nil {
		return nil
	}
	for _, ps := range snap.Providers {
		if strings.EqualFold(ps.Id, id) {
			return ps
		}
	}
	return nil
}

// CalculateWindowHeight вычисляет высоту окна по активным провайдерам из конфига
func CalculateWindowHeight(snap *model.Snapshot, cfg *config.Config) int32 {
	if cfg == nil {
		return 402
	}
	providers := cfg.EffectiveProviders()
	h := int32(6) // root margin top
	cardCount := 0

	for _, p := range providers {
		if !p.Enabled {
			continue
		}
		cardCount++
		if p.Type == "kimi" {
			h += 94
		} else if p.Type == "openrouter" {
			hasFree := true // по умолчанию есть free
			st := findProviderState(snap, p.Id)
			if st != nil && st.OpenRouter != nil {
				hasFree = (st.OpenRouter.FreeDailyLimit > 0)
			}
			if hasFree {
				h += 91
			} else {
				h += 62
			}
		} else if p.Type == "agy" {
			h += 186
		}
	}

	if cardCount > 1 {
		h += int32(cardCount-1) * 6
	}
	h += 13 // root margin bottom

	if h < 150 {
		h = 150
	}
	if h > 650 {
		h = 650
	}
	return h
}

// Render выполняет полную отрисовку карточек в 32-битный буфер
func (r *Renderer) Render(snap *model.Snapshot, cfg *config.Config) error {
	// Очищаем прозрачным цветом (alpha=0)
	procGdipGraphicsClear.Call(r.graphics, 0)

	cardX := float32(6)
	cardW := float32(r.width - 12) // 278
	contentX := cardX + 10          // 16
	contentW := cardW - 20          // 258
	curY := float32(6)              // root margin top

	providers := cfg.EffectiveProviders()

	for i, p := range providers {
		if !p.Enabled {
			continue
		}

		switch p.Type {
		case "kimi":
			st := findProviderState(snap, p.Id)
			status := "online"
			updated := ""
			k7dPct, k7dFrac := 0.0, 0.0
			k7dReset := "--"
			k5hPct, k5hFrac := 0.0, 0.0
			k5hReset := "--"

			if st != nil {
				status = st.Status
				updated = st.LastUpdated
				if st.Kimi != nil {
					k7dPct = st.Kimi.Limit7d.UsedPercent
					k7dFrac = st.Kimi.Limit7d.UsedFraction
					k7dReset = st.Kimi.Limit7d.ResetIn

					k5hPct = st.Kimi.Limit5h.UsedPercent
					k5hFrac = st.Kimi.Limit5h.UsedFraction
					k5hReset = st.Kimi.Limit5h.ResetIn
				}
			}

			cardH := float32(94)
			r.drawCardBackground(cardX, curY, cardW, cardH)

			// Шапка: [Title] ................... [Время/статус]
			title := p.Title
			if title == "" {
				title = "KIMI"
			}
			r.drawString(title, contentX, curY+8, 120, 16, r.fontTitle, colorHeader, false)
			timeText, timeColor := getRelativeTimeString(updated, status)
			r.drawString(timeText, contentX+contentW-120, curY+9, 120, 16, r.fontMeta, timeColor, true)

			// Строка 1: Kimi 7d (сверху)
			r7dY := curY + 28
			r.drawString(formatResetTimeLabel(k7dReset), contentX, r7dY, 150, 16, r.fontRow, colorLabel, false)
			pct7dColor, pct7dFont := getPercentColorAndFont(k7dPct, k7dFrac, r)
			r.drawString(formatPercent(k7dPct), contentX+contentW-80, r7dY, 80, 16, pct7dFont, pct7dColor, true)
			r.drawProgressBar(contentX, curY+45, contentW, barHeight, k7dFrac)

			// Строка 2: Kimi 5h
			r5hY := curY + 58
			r.drawString(formatResetTimeLabel(k5hReset), contentX, r5hY, 150, 16, r.fontRow, colorLabel, false)
			pct5hColor, pct5hFont := getPercentColorAndFont(k5hPct, k5hFrac, r)
			r.drawString(formatPercent(k5hPct), contentX+contentW-80, r5hY, 80, 16, pct5hFont, pct5hColor, true)
			r.drawProgressBar(contentX, curY+75, contentW, barHeight, k5hFrac)

			curY += cardH

		case "openrouter":
			st := findProviderState(snap, p.Id)
			status := "online"
			updated := ""
			orPct, orFrac := 0.0, 0.0
			orReset := "--"
			orBalance := 0.0
			orLimit := 0.0
			freeUsed := 0
			freeLimit := 0
			hasFree := false

			if st != nil {
				status = st.Status
				updated = st.LastUpdated
				if st.OpenRouter != nil {
					orPct = st.OpenRouter.UsedPercent
					orFrac = st.OpenRouter.UsedFraction
					orReset = st.OpenRouter.ResetIn
					orBalance = st.OpenRouter.LimitRemaining
					orLimit = st.OpenRouter.Limit
					freeUsed = st.OpenRouter.FreeDailyUsed
					freeLimit = st.OpenRouter.FreeDailyLimit
					hasFree = (freeLimit > 0)
				}
			}

			cardH := float32(91)
			if !hasFree {
				cardH = float32(62)
			}
			r.drawCardBackground(cardX, curY, cardW, cardH)

			// Шапка: [Title] [ост. $X.XX] ................... [Время/статус]
			title := p.Title
			if title == "" {
				title = "OPENROUTER"
			}
			r.drawString(title, contentX, curY+8, 120, 16, r.fontTitle, colorHeader, false)
			titleWidth := r.measureStringWidth(title, r.fontTitle)
			if status != "no_key" && orLimit > 0 {
				balanceText := fmt.Sprintf("ост. $%.2f", orBalance)
				r.drawString(balanceText, contentX+titleWidth+8, curY+9, 90, 16, r.fontMeta, colorLabel, false)
			}
			timeText, timeColor := getRelativeTimeString(updated, status)
			r.drawString(timeText, contentX+contentW-120, curY+9, 120, 16, r.fontMeta, timeColor, true)

			// Строка 1: Месячный лимит
			rMainY := curY + 28
			r.drawString(formatResetTimeLabel(orReset), contentX, rMainY, 150, 16, r.fontRow, colorLabel, false)
			pctColor, pctFont := getPercentColorAndFont(orPct, orFrac, r)
			r.drawString(formatPercent(orPct), contentX+contentW-80, rMainY, 80, 16, pctFont, pctColor, true)
			r.drawProgressBar(contentX, curY+45, contentW, barHeight, orFrac)

			// Строка 2: Free Daily
			if hasFree {
				rFreeY := curY + 55
				freeFrac := 0.0
				freePct := 0.0
				if freeLimit > 0 {
					freeFrac = math.Max(0.0, math.Min(1.0, float64(freeUsed)/float64(freeLimit)))
					freePct = math.Round(freeFrac*1000.0) / 10.0
				}
				r.drawString(fmt.Sprintf("free: %d/%d", freeUsed, freeLimit), contentX, rFreeY, 150, 16, r.fontRow, colorLabel, false)
				fPctColor, fPctFont := getPercentColorAndFont(freePct, freeFrac, r)
				r.drawString(formatPercent(freePct), contentX+contentW-80, rFreeY, 80, 16, fPctFont, fPctColor, true)
				r.drawProgressBar(contentX, curY+72, contentW, barHeight, freeFrac)
			}

			curY += cardH

		case "agy":
			st := findProviderState(snap, p.Id)
			status := "online"
			updated := ""
			g7dPct, g7dFrac, g7dReset := 0.0, 0.0, "--"
			g5hPct, g5hFrac, g5hReset := 0.0, 0.0, "--"
			c7dPct, c7dFrac, c7dReset := 0.0, 0.0, "--"
			c5hPct, c5hFrac, c5hReset := 0.0, 0.0, "--"

			if snap != nil {
				status = snap.AgyStatus
				if status == "" {
					status = snap.Status
				}
				updated = snap.AgyLastUpdated
				if updated == "" {
					updated = snap.LastUpdated
				}
				g7dPct = snap.GeminiWeekly.UsedPercent
				g7dFrac = snap.GeminiWeekly.UsedFraction
				g7dReset = snap.GeminiWeekly.ResetIn

				g5hPct = snap.Gemini5h.UsedPercent
				g5hFrac = snap.Gemini5h.UsedFraction
				g5hReset = snap.Gemini5h.ResetIn

				c7dPct = snap.ClaudeWeekly.UsedPercent
				c7dFrac = snap.ClaudeWeekly.UsedFraction
				c7dReset = snap.ClaudeWeekly.ResetIn

				c5hPct = snap.Claude5h.UsedPercent
				c5hFrac = snap.Claude5h.UsedFraction
				c5hReset = snap.Claude5h.ResetIn
			} else if st != nil {
				status = st.Status
				updated = st.LastUpdated
			}

			cardH := float32(186)
			r.drawCardBackground(cardX, curY, cardW, cardH)

			// Шапка GEMINI: [GEMINI] ................... [Время/статус]
			r.drawString("GEMINI", contentX, curY+8, 120, 16, r.fontTitle, colorHeader, false)
			timeText, timeColor := getRelativeTimeString(updated, status)
			r.drawString(timeText, contentX+contentW-120, curY+9, 120, 16, r.fontMeta, timeColor, true)

			// Gemini 7d (Weekly first)
			g7dY := curY + 28
			r.drawString(formatResetTimeLabel(g7dReset), contentX, g7dY, 150, 16, r.fontRow, colorLabel, false)
			g7dColor, g7dFont := getPercentColorAndFont(g7dPct, g7dFrac, r)
			r.drawString(formatPercent(g7dPct), contentX+contentW-80, g7dY, 80, 16, g7dFont, g7dColor, true)
			r.drawProgressBar(contentX, curY+45, contentW, barHeight, g7dFrac)

			// Gemini 5h
			g5hY := curY + 58
			r.drawString(formatResetTimeLabel(g5hReset), contentX, g5hY, 150, 16, r.fontRow, colorLabel, false)
			g5hColor, g5hFont := getPercentColorAndFont(g5hPct, g5hFrac, r)
			r.drawString(formatPercent(g5hPct), contentX+contentW-80, g5hY, 80, 16, g5hFont, g5hColor, true)
			r.drawProgressBar(contentX, curY+75, contentW, barHeight, g5hFrac)

			// Divider: Color.FromArgb(0x18, 0, 0, 0), Height 0.8
			divY := curY + 89
			var divPen uintptr
			procGdipCreatePen1.Call(uintptr(colorDivider), fbits(0.8), UnitPixel, uintptr(unsafe.Pointer(&divPen)))
			procGdipDrawLine.Call(r.graphics, divPen, fbits(contentX), fbits(divY), fbits(contentX+contentW), fbits(divY))
			procGdipDeletePen.Call(divPen)

			// Шапка CLAUDE/GPT (без времени справа)
			r.drawString("CLAUDE/GPT", contentX, curY+97, 120, 16, r.fontTitle, colorHeader, false)

			// Claude 7d (Weekly first)
			c7dY := curY + 117
			r.drawString(formatResetTimeLabel(c7dReset), contentX, c7dY, 150, 16, r.fontRow, colorLabel, false)
			c7dColor, c7dFont := getPercentColorAndFont(c7dPct, c7dFrac, r)
			r.drawString(formatPercent(c7dPct), contentX+contentW-80, c7dY, 80, 16, c7dFont, c7dColor, true)
			r.drawProgressBar(contentX, curY+134, contentW, barHeight, c7dFrac)

			// Claude 5h
			c5hY := curY + 147
			r.drawString(formatResetTimeLabel(c5hReset), contentX, c5hY, 150, 16, r.fontRow, colorLabel, false)
			c5hColor, c5hFont := getPercentColorAndFont(c5hPct, c5hFrac, r)
			r.drawString(formatPercent(c5hPct), contentX+contentW-80, c5hY, 80, 16, c5hFont, c5hColor, true)
			r.drawProgressBar(contentX, curY+164, contentW, barHeight, c5hFrac)

			curY += cardH
		}

		if i < len(providers)-1 {
			curY += 6 // gap between cards
		}
	}

	return nil
}
