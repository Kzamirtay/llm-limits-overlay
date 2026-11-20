package trayui

import (
	"image"
	"math"
	"testing"
)

var testPalette = Palette{
	Track:  RGB{0x80, 0x80, 0x80},
	Arc:    RGB{0x10, 0x10, 0x10},
	TrackA: 160,
	ArcA:   255,
}

// Углы: 12 часов → 0, 3 часа → π/2, 6 часов → π, 9 часов → 3π/2.
func TestAngleFromTop(t *testing.T) {
	cases := []struct {
		dx, dy, want float64
	}{
		{0, -1, 0},               // вверх
		{1, 0, math.Pi / 2},      // вправо
		{0, 1, math.Pi},          // вниз
		{-1, 0, 3 * math.Pi / 2}, // влево
	}
	for _, c := range cases {
		if got := angleFromTop(c.dx, c.dy); math.Abs(got-c.want) > 1e-9 {
			t.Errorf("angleFromTop(%v,%v) = %v, want %v", c.dx, c.dy, got, c.want)
		}
	}
}

// Пустая доля — весь трек, полная — вся дуга.
func TestBadge_EmptyAndFull(t *testing.T) {
	const size = 32
	empty := Badge(size, 0, StateOK, testPalette, "")
	full := Badge(size, 1, StateOK, testPalette, "")

	// Точка справа (3 часа) — на дуге при 100%, на треке при 0%.
	if px := pixelAt(full, size/2+size/3, size/2); px[3] == 0 {
		t.Fatal("full badge: right side of ring is transparent")
	}
	rEmpty := pixelAt(empty, size/2+size/3, size/2)
	rFull := pixelAt(full, size/2+size/3, size/2)
	if rgbSum(rEmpty) <= rgbSum(rFull) {
		t.Fatalf("track %v is not lighter than arc %v", rEmpty, rFull)
	}

	// Центр обязан остаться прозрачным: значок — кольцо, а не диск.
	if a := pixelAt(full, size/2, size/2)[3]; a != 0 {
		t.Fatalf("center alpha = %d, want 0 (ring must keep hole)", a)
	}
	// Углы тоже прозрачны.
	if a := pixelAt(full, 0, 0)[3]; a != 0 {
		t.Fatalf("corner alpha = %d, want 0", a)
	}
}

// Дуга растёт по часовой стрелке от 12 часов: при 25% правый верх занят,
// а левый верх ещё трек.
func TestBadge_ArcGrowsClockwise(t *testing.T) {
	const size = 40
	img := Badge(size, 0.25, StateOK, testPalette, "")
	c := size / 2
	upperLeft := pixelAt(img, c-size/4, c-size/3)
	upperRight := pixelAt(img, c+size/4, c-size/3)
	if rgbSum(upperLeft) <= rgbSum(upperRight) {
		t.Fatalf("upper-left %v should be track (lighter) and upper-right %v arc", upperLeft, upperRight)
	}
}

// StateUnknown и отсутствие данных: дуги нет ни при каком проценте.
func TestBadge_UnknownDrawsTrackOnly(t *testing.T) {
	const size = 32
	unknown := Badge(size, 100, StateUnknown, testPalette, "")
	if a := pixelAt(unknown, size/2+size/3, size/2)[3]; a == 0 {
		t.Fatal("track must still be drawn for unknown state")
	}
	known := Badge(size, 100, StateOK, testPalette, "")
	if meanRGB(known) >= meanRGB(unknown) {
		t.Fatalf("known full-arc icon (mean %v) should be darker than track-only (mean %v)", meanRGB(known), meanRGB(unknown))
	}
}

func TestBadge_FractionIsClamped(t *testing.T) {
	const size = 24
	neg := Badge(size, -5, StateOK, testPalette, "")
	over := Badge(size, 500, StateOK, testPalette, "")
	zero := Badge(size, 0, StateOK, testPalette, "")
	hundred := Badge(size, 1, StateOK, testPalette, "")
	if string(neg.Pix) != string(zero.Pix) {
		t.Error("fraction < 0 must render like 0")
	}
	if string(over.Pix) != string(hundred.Pix) {
		t.Error("fraction > 1 must render like 1")
	}
}

func TestQuantizePercent(t *testing.T) {
	cases := []struct{ in, step, want float64 }{
		{97.3, 5, 95},
		{98, 5, 100},
		{100, 5, 100},
		{0, 5, 0},
		{-3, 5, 0},
		{123, 5, 100},
		{42.4, 1, 42},
		{42.4, 0, 42.4},
	}
	for _, c := range cases {
		if got := QuantizePercent(c.in, c.step); math.Abs(got-c.want) > 1e-9 {
			t.Errorf("QuantizePercent(%v, %v) = %v, want %v", c.in, c.step, got, c.want)
		}
	}
}

// Размеры значка должны быть квадратными и покрывать весь DPI-диапазон.
func TestBadge_AllSizesRender(t *testing.T) {
	for _, size := range BadgeSizes {
		img := Badge(size, 0.5, StateOK, testPalette, "K")
		if img.Rect.Dx() != size || img.Rect.Dy() != size {
			t.Fatalf("size %d: got %v", size, img.Rect)
		}
		if !hasOpaque(img) {
			t.Fatalf("size %d: icon is fully transparent", size)
		}
	}
}

// Буква нужна, чтобы различать значки провайдеров: без неё центр прозрачен.
func TestBadge_GlyphInCenter(t *testing.T) {
	const size = 16
	c := size / 2
	for _, glyph := range []string{"K", "O", "G"} {
		img := Badge(size, 0.5, StateOK, testPalette, glyph)
		if !hasOpaqueInHole(img, c, 4) {
			t.Errorf("glyph %q: hole is empty", glyph)
		}
	}
	if a := pixelAt(Badge(size, 0.5, StateOK, testPalette, ""), c, c)[3]; a != 0 {
		t.Errorf("no glyph: center alpha = %d, want 0", a)
	}
}

// Буквы различаются между собой — иначе смысл теряется.
func TestBadge_GlyphsDiffer(t *testing.T) {
	const size = 16
	seen := map[string]string{}
	for _, glyph := range []string{"K", "O", "G", "R", "X", "C"} {
		img := Badge(size, 0, StateOK, testPalette, glyph)
		key := string(img.Pix)
		if prev, ok := seen[key]; ok {
			t.Fatalf("glyphs %q and %q render identically", prev, glyph)
		}
		seen[key] = glyph
	}
}

// Буква, которой нет в шрифте, не рисуется и не ломает кольцо.
func TestBadge_UnknownGlyphIgnored(t *testing.T) {
	const size = 16
	withBad := Badge(size, 0.5, StateOK, testPalette, "Ж")
	plain := Badge(size, 0.5, StateOK, testPalette, "")
	if string(withBad.Pix) != string(plain.Pix) {
		t.Error("unsupported glyph must not change the icon")
	}
}

// Буква не должна заезжать на кольцо: дуга обязана оставаться видимой.
func TestBadge_GlyphStaysInsideHole(t *testing.T) {
	const size = 24
	img := Badge(size, 1, StateOK, testPalette, "O")
	// Кольцо при 100% сплошное: любая дырка на радиусе кольца — ошибка.
	c := size / 2
	for angleStep := 0; angleStep < 360; angleStep += 15 {
		a := float64(angleStep) * math.Pi / 180
		r := float64(size)/2 - 2
		x := int(math.Round(float64(c) + r*math.Sin(a)))
		y := int(math.Round(float64(c) - r*math.Cos(a)))
		if pixelAt(img, x, y)[3] == 0 {
			t.Fatalf("ring has a hole at %d° (%d,%d)", angleStep, x, y)
		}
	}
}

func TestGlyphFor(t *testing.T) {
	cases := []struct{ provType, label, want string }{
		{"kimi", "KIMI", "K"},
		{"openrouter", "OPENROUTER", "R"},
		{"opencode", "OPENCODE GO", "O"},
		{"codex", "CODEX", "X"},
		{"agy", "GEMINI", "G"},
		{"agy", "CLAUDE/GPT", "C"},
		{"custom", "MYPROVIDER", "M"},
		{"custom", "свой", ""},
	}
	for _, c := range cases {
		if got := GlyphFor(c.provType, c.label); got != c.want {
			t.Errorf("GlyphFor(%q,%q) = %q, want %q", c.provType, c.label, got, c.want)
		}
	}
	if !GlyphSupported("K") {
		t.Error("K must be in the font")
	}
	if GlyphSupported("Ж") || GlyphSupported("KG") {
		t.Error("Ж and multi-char strings are not single supported glyphs")
	}
}

// hasOpaqueInHole — есть ли непрозрачные пиксели в отверстии радиуса r.
func hasOpaqueInHole(img *image.NRGBA, c, r int) bool {
	for y := c - r; y <= c+r; y++ {
		for x := c - r; x <= c+r; x++ {
			if px := pixelAt(img, x, y); px[3] != 0 {
				return true
			}
		}
	}
	return false
}

func pixelAt(img *image.NRGBA, x, y int) [4]uint8 {
	i := img.PixOffset(x, y)
	return [4]uint8{img.Pix[i], img.Pix[i+1], img.Pix[i+2], img.Pix[i+3]}
}

func rgbSum(p [4]uint8) int { return int(p[0]) + int(p[1]) + int(p[2]) }

func meanRGB(img *image.NRGBA) float64 {
	var sum, n float64
	for i := 0; i < len(img.Pix); i += 4 {
		if img.Pix[i+3] == 0 {
			continue
		}
		sum += float64(img.Pix[i]) + float64(img.Pix[i+1]) + float64(img.Pix[i+2])
		n++
	}
	if n == 0 {
		return 0
	}
	return sum / n
}

func hasOpaque(img *image.NRGBA) bool {
	for i := 3; i < len(img.Pix); i += 4 {
		if img.Pix[i] != 0 {
			return true
		}
	}
	return false
}
