package main

import (
	"fmt"
	"math"
	"strings"
	"time"
	"unsafe"

	"llmlimits/internal/config"
	"llmlimits/internal/fetchers"
	"llmlimits/internal/model"

	"golang.org/x/sys/windows"
)

type Renderer struct {
	width  int32
	height int32

	memDC    uintptr
	hbmp     uintptr
	bits     unsafe.Pointer
	gpBitmap uintptr
	graphics uintptr

	family     uintptr
	fontTitle  uintptr // 11px Bold
	fontText   uintptr // 10px Regular
	fontSub    uintptr // 9px Regular
	fmtNear    uintptr
	fmtFar     uintptr
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

	var fontTitle, fontText, fontSub uintptr
	procGdipCreateFont.Call(family, fbits(11.0), 1, UnitPixel, uintptr(unsafe.Pointer(&fontTitle))) // 11px Bold
	procGdipCreateFont.Call(family, fbits(10.0), 0, UnitPixel, uintptr(unsafe.Pointer(&fontText)))  // 10px Regular
	procGdipCreateFont.Call(family, fbits(9.0), 0, UnitPixel, uintptr(unsafe.Pointer(&fontSub)))   // 9px Regular

	var fmtNear, fmtFar uintptr
	procGdipCreateStringFormat.Call(0, 0, uintptr(unsafe.Pointer(&fmtNear)))
	procGdipCreateStringFormat.Call(0, 0, uintptr(unsafe.Pointer(&fmtFar)))
	procGdipSetStringFormatAlign.Call(fmtFar, 2) // Far = Right

	return &Renderer{
		width:      w,
		height:     h,
		memDC:      memDC,
		hbmp:       hbmp,
		bits:       bits,
		gpBitmap:   gpBitmap,
		graphics:   g,
		family:     family,
		fontTitle:  fontTitle,
		fontText:   fontText,
		fontSub:    fontSub,
		fmtNear:    fmtNear,
		fmtFar:     fmtFar,
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
	if r.fontText != 0 {
		procGdipDeleteFont.Call(r.fontText)
	}
	if r.fontSub != 0 {
		procGdipDeleteFont.Call(r.fontSub)
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

func (r *Renderer) drawProgressBar(x, y, w, h float32, usedFraction float64, level string) {
	trackColor := argb(20, 255, 255, 255) // rgba(255,255,255,0.08)
	r.drawRoundedRect(x, y, w, h, h/2, trackColor, 0)

	if usedFraction <= 0 {
		return
	}
	if usedFraction > 1.0 {
		usedFraction = 1.0
	}
	fillW := float32(float64(w) * usedFraction)
	if fillW < h {
		fillW = h
	}

	fillColor := argb(255, 16, 185, 129) // Emerald default
	if level == "critical" || usedFraction >= 0.95 {
		fillColor = argb(255, 239, 68, 68) // Red
	} else if level == "warning" || usedFraction >= 0.80 {
		fillColor = argb(255, 245, 158, 11) // Amber
	} else {
		fillColor = argb(255, 56, 189, 248) // Sky blue
	}

	r.drawRoundedRect(x, y, fillW, h, h/2, fillColor, 0)
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

func formatRuTimeDiff(diff string) string {
	if diff == "" || diff == "--" || diff == "N/A" {
		return "--"
	}
	s := strings.ReplaceAll(diff, "d", "д")
	s = strings.ReplaceAll(s, "h", "ч")
	s = strings.ReplaceAll(s, "m", "м")
	return s
}

func getRelativeTimeString(updatedIso, status string) string {
	if status == "no_key" {
		return "нет ключа"
	}
	if status == "error" && updatedIso == "" {
		return "ошибка"
	}
	if updatedIso == "" {
		return "нет данных"
	}

	t, err := time.Parse("2006-01-02 15:04:05", updatedIso)
	if err != nil {
		t, err = time.Parse(time.RFC3339, updatedIso)
	}
	if err != nil {
		return "только что"
	}

	mins := int(time.Since(t).Minutes())
	if mins < 0 {
		mins = 0
	}

	var base string
	if mins == 0 {
		base = "только что"
	} else if mins < 60 {
		base = fmt.Sprintf("%d мин", mins)
	} else {
		base = fmt.Sprintf("%dч", mins/60)
	}

	if status == "stale" {
		base += " (stale)"
	}
	return base
}

// Render выполняет полную отрисовку карточек в 32-битный буфер
func (r *Renderer) Render(snap *model.Snapshot, cfg *config.Config) error {
	// Очищаем прозрачным цветом
	procGdipGraphicsClear.Call(r.graphics, 0)

	cardBg := argb(178, 30, 41, 59)       // rgba(30, 41, 59, 0.70)
	cardBorder := argb(38, 148, 163, 184) // rgba(148, 163, 184, 0.15)
	textMuted := argb(255, 148, 163, 184) // #94a3b8
	textLabel := argb(255, 203, 213, 225) // #cbd5e1

	colorKimi := argb(255, 16, 185, 129)       // Emerald #10b981
	colorOpenRouter := argb(255, 168, 85, 247) // Purple #a855f7
	colorGemini := argb(255, 59, 130, 246)     // Blue #3b82f6
	colorClaude := argb(255, 249, 115, 22)     // Orange #f97316

	cardX := float32(10)
	cardW := float32(r.width - 20) // 270
	curY := float32(10)

	// Найдём состояния провайдеров
	var kimiState, orState, agyState *model.ProviderState
	if snap != nil {
		for _, p := range snap.Providers {
			switch p.Id {
			case "kimi":
				kimiState = p
			case "openrouter", "or", "or2":
				if orState == nil {
					orState = p
				}
			case "agy":
				agyState = p
			}
		}
	}

	// 1. Карточка KIMI (высота ~96px)
	cardH1 := float32(96)
	r.drawRoundedRect(cardX, curY, cardW, cardH1, 10, cardBg, cardBorder)

	// KIMI Header
	kimiStatus := "online"
	kimiUpdated := ""
	if kimiState != nil {
		kimiStatus = kimiState.Status
		kimiUpdated = kimiState.LastUpdated
	}
	r.drawString("KIMI", cardX+12, curY+10, 100, 16, r.fontTitle, colorKimi, false)
	r.drawString(getRelativeTimeString(kimiUpdated, kimiStatus), cardX+cardW-120, curY+11, 108, 16, r.fontText, textMuted, true)

	// KIMI 7d Row
	k7dPct := 0.0
	k7dFrac := 0.0
	k7dReset := "--"
	if kimiState != nil && kimiState.Kimi != nil {
		k7dPct = kimiState.Kimi.Limit7d.UsedPercent
		k7dFrac = kimiState.Kimi.Limit7d.UsedFraction
		k7dReset = formatRuTimeDiff(kimiState.Kimi.Limit7d.ResetIn)
	}
	r.drawString("Лимит 7d", cardX+12, curY+30, 100, 14, r.fontText, textLabel, false)
	r.drawString(fmt.Sprintf("%.1f%% • %s", k7dPct, k7dReset), cardX+cardW-140, curY+30, 128, 14, r.fontText, textLabel, true)
	r.drawProgressBar(cardX+12, curY+47, cardW-24, 5.5, k7dFrac, fetchers.GetUsedStatusLevel(k7dFrac))

	// KIMI 5h Row
	k5hPct := 0.0
	k5hFrac := 0.0
	k5hReset := "--"
	if kimiState != nil && kimiState.Kimi != nil {
		k5hPct = kimiState.Kimi.Limit5h.UsedPercent
		k5hFrac = kimiState.Kimi.Limit5h.UsedFraction
		k5hReset = formatRuTimeDiff(kimiState.Kimi.Limit5h.ResetIn)
	}
	r.drawString("Лимит 5h", cardX+12, curY+60, 100, 14, r.fontText, textLabel, false)
	r.drawString(fmt.Sprintf("%.1f%% • %s", k5hPct, k5hReset), cardX+cardW-140, curY+60, 128, 14, r.fontText, textLabel, true)
	r.drawProgressBar(cardX+12, curY+77, cardW-24, 5.5, k5hFrac, fetchers.GetUsedStatusLevel(k5hFrac))

	curY += cardH1 + 6

	// 2. Карточка OPENROUTER (высота ~96px)
	cardH2 := float32(96)
	r.drawRoundedRect(cardX, curY, cardW, cardH2, 10, cardBg, cardBorder)

	// Header: OPENROUTER + ост. $X.XX + time
	orStatus := "online"
	orUpdated := ""
	orBalance := 0.0
	if orState != nil {
		orStatus = orState.Status
		orUpdated = orState.LastUpdated
		if orState.OpenRouter != nil {
			orBalance = orState.OpenRouter.LimitRemaining
		}
	}
	r.drawString("OPENROUTER", cardX+12, curY+10, 95, 16, r.fontTitle, colorOpenRouter, false)
	if orStatus != "no_key" && orBalance > 0 {
		r.drawString(fmt.Sprintf("ост. $%.2f", orBalance), cardX+110, curY+11, 75, 16, r.fontText, textMuted, false)
	}
	r.drawString(getRelativeTimeString(orUpdated, orStatus), cardX+cardW-110, curY+11, 98, 16, r.fontText, textMuted, true)

	// OR Main Row
	orPct := 0.0
	orFrac := 0.0
	orReset := "--"
	if orState != nil && orState.OpenRouter != nil {
		orPct = orState.OpenRouter.UsedPercent
		orFrac = orState.OpenRouter.UsedFraction
		orReset = formatRuTimeDiff(orState.OpenRouter.ResetIn)
	}
	r.drawString("Месячный лимит", cardX+12, curY+30, 100, 14, r.fontText, textLabel, false)
	r.drawString(fmt.Sprintf("%.1f%% • %s", orPct, orReset), cardX+cardW-140, curY+30, 128, 14, r.fontText, textLabel, true)
	r.drawProgressBar(cardX+12, curY+47, cardW-24, 5.5, orFrac, fetchers.GetUsedStatusLevel(orFrac))

	// OR Free Row
	orFreeUsed := 0
	orFreeLimit := 1000
	orFreeFrac := 0.0
	orFreePct := 0.0
	if orState != nil && orState.OpenRouter != nil {
		orFreeUsed = orState.OpenRouter.FreeDailyUsed
		if orState.OpenRouter.FreeDailyLimit > 0 {
			orFreeLimit = orState.OpenRouter.FreeDailyLimit
		}
		if orFreeLimit > 0 {
			orFreeFrac = float64(orFreeUsed) / float64(orFreeLimit)
			orFreePct = math.Round(orFreeFrac*1000) / 10
		}
	}
	r.drawString(fmt.Sprintf("free: %d/%d", orFreeUsed, orFreeLimit), cardX+12, curY+60, 110, 14, r.fontText, textLabel, false)
	r.drawString(fmt.Sprintf("%.1f%%", orFreePct), cardX+cardW-70, curY+60, 58, 14, r.fontText, textLabel, true)
	r.drawProgressBar(cardX+12, curY+77, cardW-24, 5.5, orFreeFrac, "ok")

	curY += cardH2 + 6

	// 3. Карточка AGY: GEMINI + CLAUDE/GPT (высота ~178px)
	cardH3 := float32(178)
	r.drawRoundedRect(cardX, curY, cardW, cardH3, 10, cardBg, cardBorder)

	agyStatusStr := "online"
	agyUpdated := ""
	if agyState != nil {
		agyStatusStr = agyState.Status
		agyUpdated = agyState.LastUpdated
	}
	r.drawString("GEMINI", cardX+12, curY+10, 95, 16, r.fontTitle, colorGemini, false)
	r.drawString(getRelativeTimeString(agyUpdated, agyStatusStr), cardX+cardW-120, curY+11, 108, 16, r.fontText, textMuted, true)

	// Gemini 7d (Weekly first)
	g7dPct := 0.0
	g7dFrac := 0.0
	g7dReset := "--"
	if snap != nil {
		g7dPct = snap.GeminiWeekly.UsedPercent
		g7dFrac = snap.GeminiWeekly.UsedFraction
		g7dReset = formatRuTimeDiff(snap.GeminiWeekly.ResetIn)
	}
	r.drawString("Лимит 7d", cardX+12, curY+29, 100, 14, r.fontText, textLabel, false)
	r.drawString(fmt.Sprintf("%.1f%% • %s", g7dPct, g7dReset), cardX+cardW-140, curY+29, 128, 14, r.fontText, textLabel, true)
	r.drawProgressBar(cardX+12, curY+45, cardW-24, 5.5, g7dFrac, fetchers.GetUsedStatusLevel(g7dFrac))

	// Gemini 5h
	g5hPct := 0.0
	g5hFrac := 0.0
	g5hReset := "--"
	if snap != nil {
		g5hPct = snap.Gemini5h.UsedPercent
		g5hFrac = snap.Gemini5h.UsedFraction
		g5hReset = formatRuTimeDiff(snap.Gemini5h.ResetIn)
	}
	r.drawString("Лимит 5h", cardX+12, curY+58, 100, 14, r.fontText, textLabel, false)
	r.drawString(fmt.Sprintf("%.1f%% • %s", g5hPct, g5hReset), cardX+cardW-140, curY+58, 128, 14, r.fontText, textLabel, true)
	r.drawProgressBar(cardX+12, curY+74, cardW-24, 5.5, g5hFrac, fetchers.GetUsedStatusLevel(g5hFrac))

	// Divider
	divY := curY + 88
	var divPen uintptr
	procGdipCreatePen1.Call(uintptr(argb(20, 255, 255, 255)), fbits(0.8), UnitPixel, uintptr(unsafe.Pointer(&divPen)))
	procGdipDrawLine.Call(r.graphics, divPen, fbits(cardX+12), fbits(divY), fbits(cardX+cardW-12), fbits(divY))
	procGdipDeletePen.Call(divPen)

	// Claude Section
	r.drawString("CLAUDE/GPT", cardX+12, curY+96, 120, 16, r.fontTitle, colorClaude, false)

	// Claude 7d (Weekly first)
	c7dPct := 0.0
	c7dFrac := 0.0
	c7dReset := "--"
	if snap != nil {
		c7dPct = snap.ClaudeWeekly.UsedPercent
		c7dFrac = snap.ClaudeWeekly.UsedFraction
		c7dReset = formatRuTimeDiff(snap.ClaudeWeekly.ResetIn)
	}
	r.drawString("Лимит 7d", cardX+12, curY+115, 100, 14, r.fontText, textLabel, false)
	r.drawString(fmt.Sprintf("%.1f%% • %s", c7dPct, c7dReset), cardX+cardW-140, curY+115, 128, 14, r.fontText, textLabel, true)
	r.drawProgressBar(cardX+12, curY+131, cardW-24, 5.5, c7dFrac, fetchers.GetUsedStatusLevel(c7dFrac))

	// Claude 5h
	c5hPct := 0.0
	c5hFrac := 0.0
	c5hReset := "--"
	if snap != nil {
		c5hPct = snap.Claude5h.UsedPercent
		c5hFrac = snap.Claude5h.UsedFraction
		c5hReset = formatRuTimeDiff(snap.Claude5h.ResetIn)
	}
	r.drawString("Лимит 5h", cardX+12, curY+144, 100, 14, r.fontText, textLabel, false)
	r.drawString(fmt.Sprintf("%.1f%% • %s", c5hPct, c5hReset), cardX+cardW-140, curY+144, 128, 14, r.fontText, textLabel, true)
	r.drawProgressBar(cardX+12, curY+160, cardW-24, 5.5, c5hFrac, fetchers.GetUsedStatusLevel(c5hFrac))

	return nil
}
