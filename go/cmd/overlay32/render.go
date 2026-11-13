//go:build windows

package main

import (
	"fmt"
	"math"
	"strings"
	"time"
	"unsafe"

	"llmlimits/internal/config"
	"llmlimits/internal/layout"
	"llmlimits/internal/model"

	"golang.org/x/sys/windows"
)

// Constants for overlay design and typography
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
	procGdipSetTextRenderingHint.Call(g, TextRenderingHintAntiAliasGridFit)

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

// formatResetTimeLabel форматирует время сброса в человекочитаемый вид
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

// getRelativeTimeString возвращает человекочитаемую строку времени обновления и цвет статуса
func getRelativeTimeString(updatedIso, status string) (string, uint32) {
	if status == "no_key" {
		return "нет ключа", colorLabel
	}
	if status == "error" {
		return "ошибка", colorMetaStale
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

// formatPercent — точный формат C#: b.Percent + "%" с запятой как в русской культуре
func formatPercent(pct float64) string {
	s := fmt.Sprintf("%.1f%%", pct)
	if strings.HasSuffix(s, ".0%") {
		return strings.Replace(s, ".0%", "%", 1)
	}
	return strings.Replace(s, ".", ",", 1)
}

// agyBucketPctText — для заглушек (нет измеренных данных) возвращает "--"
// вместо выдуманного "0%"; второе значение false означает «не рисовать бар».
func agyBucketPctText(placeholder bool, pct float64) (string, bool) {
	if placeholder {
		return "--", false
	}
	return formatPercent(pct), true
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
	h := int32(6) // root margin top
	cards := 0

	for _, p := range cfg.EffectiveProviders() {
		if !p.Enabled {
			continue
		}
		ch := layout.CardHeight(cfg, p, findProviderState(snap, p.Id), snap)
		if ch <= 0 {
			continue // все окна скрыты настройками или схлопнуты исчерпанием
		}
		h += int32(ch)
		cards++
	}

	if cards > 1 {
		h += int32(cards-1) * 6
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

// drawHeaderTitle рисует заголовок карточки и относительное время справа.
func (r *Renderer) drawHeaderTitle(title string, x, y, w float32, updated, status string) {
	r.drawString(title, x, y+8, 150, 16, r.fontTitle, colorHeader, false)
	timeText, timeColor := getRelativeTimeString(updated, status)
	r.drawString(timeText, x+w-120, y+9, 120, 16, r.fontMeta, timeColor, true)
}

// drawMeterRow — строка карточки: таймер сброса слева, процент справа, полоса
// под строкой. Заглушка (нет измеренных данных) даёт «--» без полосы.
func (r *Renderer) drawMeterRow(x, w, y float32, b model.Bucket) {
	r.drawString(formatResetTimeLabel(b.ResetIn), x, y, 150, 16, r.fontRow, colorLabel, false)
	text, ok := agyBucketPctText(b.Placeholder, b.UsedPercent)
	if !ok {
		r.drawString(text, x+w-80, y, 80, 16, r.fontRow, colorLabel, true)
		return
	}
	c, f := getPercentColorAndFont(b.UsedPercent, b.UsedFraction, r)
	r.drawString(text, x+w-80, y, 80, 16, f, c, true)
	r.drawProgressBar(x, y+17, w, barHeight, b.UsedFraction)
}

// Render выполняет полную отрисовку карточек в 32-битный буфер
func (r *Renderer) Render(snap *model.Snapshot, cfg *config.Config) error {
	// Очищаем прозрачным цветом (alpha=0)
	procGdipGraphicsClear.Call(r.graphics, 0)

	cardX := float32(6)
	cardW := float32(r.width - 12) // 278
	contentX := cardX + 10         // 16
	contentW := cardW - 20         // 258
	curY := float32(6)             // root margin top
	drawn := 0

	for _, p := range cfg.EffectiveProviders() {
		if !p.Enabled {
			continue
		}
		st := findProviderState(snap, p.Id)
		cardH := layout.CardHeight(cfg, p, st, snap)
		if cardH <= 0 {
			continue
		}
		if drawn > 0 {
			curY += 6 // gap между видимыми карточками
		}

		status := "online"
		updated := ""
		if st != nil {
			status = st.Status
			updated = st.LastUpdated
		}
		title := p.Title
		if title == "" {
			title = strings.ToUpper(p.Id)
		}

		r.drawCardBackground(cardX, curY, cardW, cardH)

		switch p.Type {
		case "kimi":
			r.drawHeaderTitle(title, contentX, curY, contentW, updated, status)
			for i, b := range layout.KimiRows(cfg, p, st) {
				r.drawMeterRow(contentX, contentW, curY+28+30*float32(i), b)
			}

		case "openrouter":
			orPct, orFrac := 0.0, 0.0
			orReset := "--"
			orLimit, orBalance := 0.0, 0.0
			freeUsed, freeLimit := 0, 0
			if st != nil && st.OpenRouter != nil {
				or := st.OpenRouter
				orPct = or.UsedPercent
				orFrac = or.UsedFraction
				orReset = or.ResetIn
				orLimit = or.Limit
				orBalance = or.LimitRemaining
				freeUsed = or.FreeDailyUsed
				freeLimit = or.FreeDailyLimit
			}

			r.drawString(title, contentX, curY+8, 150, 16, r.fontTitle, colorHeader, false)
			titleWidth := r.measureStringWidth(title, r.fontTitle)
			showLimit := layout.OpenRouterShowLimit(cfg, p)
			if showLimit && status != "no_key" && orLimit > 0 {
				balanceText := fmt.Sprintf("ост. $%.2f", orBalance)
				r.drawString(balanceText, contentX+titleWidth+8, curY+9, 90, 16, r.fontMeta, colorLabel, false)
			}
			timeText, timeColor := getRelativeTimeString(updated, status)
			r.drawString(timeText, contentX+contentW-120, curY+9, 120, 16, r.fontMeta, timeColor, true)

			row := float32(0)
			if showLimit {
				r.drawMeterRow(contentX, contentW, curY+28, model.Bucket{
					UsedPercent:  orPct,
					UsedFraction: orFrac,
					ResetIn:      orReset,
				})
				row++
			}
			if layout.OpenRouterShowFree(cfg, p, st) {
				freeY := curY + 28 + 27*row
				freeFrac := 0.0
				freePct := 0.0
				if freeLimit > 0 {
					freeFrac = math.Max(0.0, math.Min(1.0, float64(freeUsed)/float64(freeLimit)))
					freePct = math.Round(freeFrac*1000.0) / 10.0
				}
				r.drawString(fmt.Sprintf("free: %d/%d", freeUsed, freeLimit), contentX, freeY, 150, 16, r.fontRow, colorLabel, false)
				fPctColor, fPctFont := getPercentColorAndFont(freePct, freeFrac, r)
				r.drawString(formatPercent(freePct), contentX+contentW-80, freeY, 80, 16, fPctFont, fPctColor, true)
				r.drawProgressBar(contentX, freeY+17, contentW, barHeight, freeFrac)
			}

		case "codex":
			planType, balance := "", ""
			if st != nil && st.Codex != nil {
				planType = st.Codex.PlanType
				balance = st.Codex.Balance
			}

			r.drawString(title, contentX, curY+8, 150, 16, r.fontTitle, colorHeader, false)
			titleWidth := r.measureStringWidth(title, r.fontTitle)
			metaParts := []string{}
			if planType != "" {
				metaParts = append(metaParts, planType)
			}
			if balance != "" && balance != "$0" && balance != "0" {
				metaParts = append(metaParts, balance)
			}
			if len(metaParts) > 0 && status != "no_key" && status != "error" {
				metaText := strings.Join(metaParts, " • ")
				r.drawString(metaText, contentX+titleWidth+8, curY+9, 100, 16, r.fontMeta, colorLabel, false)
			}
			timeText, timeColor := getRelativeTimeString(updated, status)
			r.drawString(timeText, contentX+contentW-120, curY+9, 120, 16, r.fontMeta, timeColor, true)

			for i, b := range layout.CodexRows(cfg, p, st) {
				r.drawMeterRow(contentX, contentW, curY+28+32*float32(i), b)
			}

		case "opencode":
			r.drawHeaderTitle(title, contentX, curY, contentW, updated, status)
			for i, w := range layout.OpenCodeWindows(cfg, p, st) {
				r.drawMeterRow(contentX, contentW, curY+28+28*float32(i), w.Bucket)
			}

		case "agy":
			agyStatus := status
			agyUpdated := updated
			var gWeekly, g5h, cWeekly, c5h model.Bucket
			if snap != nil {
				if snap.AgyStatus != "" {
					agyStatus = snap.AgyStatus
				}
				if snap.AgyLastUpdated != "" {
					agyUpdated = snap.AgyLastUpdated
				}
				gWeekly, g5h = snap.GeminiWeekly, snap.Gemini5h
				cWeekly, c5h = snap.ClaudeWeekly, snap.Claude5h
			}
			geminiOn, claudeOn := layout.AgyShowGroups(cfg, p)
			gRows := layout.AgyRows(geminiOn, gWeekly, g5h)
			cRows := layout.AgyRows(claudeOn, cWeekly, c5h)
			sectionY := curY
			groupsDrawn := 0

			if len(gRows) > 0 {
				r.drawHeaderTitle("GEMINI", contentX, sectionY, contentW, agyUpdated, agyStatus)
				for i, b := range gRows {
					r.drawMeterRow(contentX, contentW, sectionY+28+30*float32(i), b)
				}
				sectionY += layout.AgyGroupHeight(len(gRows))
				groupsDrawn++
			}
			if len(cRows) > 0 {
				if groupsDrawn > 0 {
					var divPen uintptr
					procGdipCreatePen1.Call(uintptr(colorDivider), fbits(0.8), UnitPixel, uintptr(unsafe.Pointer(&divPen)))
					procGdipDrawLine.Call(r.graphics, divPen, fbits(contentX), fbits(sectionY), fbits(contentX+contentW), fbits(sectionY))
					procGdipDeletePen.Call(divPen)
					sectionY += 8
					r.drawString("CLAUDE/GPT", contentX, sectionY+8, 120, 16, r.fontTitle, colorHeader, false)
				} else {
					r.drawHeaderTitle("CLAUDE/GPT", contentX, sectionY, contentW, agyUpdated, agyStatus)
				}
				for i, b := range cRows {
					r.drawMeterRow(contentX, contentW, sectionY+28+30*float32(i), b)
				}
			}
		}

		curY += cardH
		drawn++
	}

	return nil
}
