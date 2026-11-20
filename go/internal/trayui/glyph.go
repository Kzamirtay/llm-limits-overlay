package trayui

import (
	"image"
	"math"
	"strings"
)

// glyphW, glyphH — размер встроенного шрифта. 5×7 — минимум, на котором
// заглавная буква остаётся читаемой при высоте отверстия кольца ~9 px
// (значок 16 px при 100 % DPI). GDI+ тут не годится: сглаженный текст
// высотой 7 px превращается в серое пятно, а битовая карта в масштабе 1:1
// остаётся резкой.
const (
	glyphW = 5
	glyphH = 7
)

// glyphs — только те буквы, которыми различаются значки провайдеров
// (плюс запас на пользовательские подписи): каждая строка — 5 бит, старший
// бит слева. Незнакомая буква просто не рисуется, и кольцо остаётся чистым.
var glyphs = map[rune][glyphH]uint8{
	'K': {0b10001, 0b10010, 0b10100, 0b11000, 0b10100, 0b10010, 0b10001},
	'O': {0b01110, 0b10001, 0b10001, 0b10001, 0b10001, 0b10001, 0b01110},
	'G': {0b01110, 0b10001, 0b10000, 0b10111, 0b10001, 0b10001, 0b01110},
	'C': {0b01110, 0b10001, 0b10000, 0b10000, 0b10000, 0b10001, 0b01110},
	'R': {0b11110, 0b10001, 0b10001, 0b11110, 0b10100, 0b10010, 0b10001},
	'X': {0b10001, 0b10001, 0b01010, 0b00100, 0b01010, 0b10001, 0b10001},
	'A': {0b01110, 0b10001, 0b10001, 0b11111, 0b10001, 0b10001, 0b10001},
	'B': {0b11110, 0b10001, 0b10001, 0b11110, 0b10001, 0b10001, 0b11110},
	'D': {0b11110, 0b10001, 0b10001, 0b10001, 0b10001, 0b10001, 0b11110},
	'E': {0b11111, 0b10000, 0b10000, 0b11110, 0b10000, 0b10000, 0b11111},
	'F': {0b11111, 0b10000, 0b10000, 0b11110, 0b10000, 0b10000, 0b10000},
	'H': {0b10001, 0b10001, 0b10001, 0b11111, 0b10001, 0b10001, 0b10001},
	'I': {0b01110, 0b00100, 0b00100, 0b00100, 0b00100, 0b00100, 0b01110},
	'L': {0b10000, 0b10000, 0b10000, 0b10000, 0b10000, 0b10000, 0b11111},
	'M': {0b10001, 0b11011, 0b10101, 0b10101, 0b10001, 0b10001, 0b10001},
	'N': {0b10001, 0b11001, 0b10101, 0b10011, 0b10001, 0b10001, 0b10001},
	'P': {0b11110, 0b10001, 0b10001, 0b11110, 0b10000, 0b10000, 0b10000},
	'S': {0b01111, 0b10000, 0b10000, 0b01110, 0b00001, 0b00001, 0b11110},
	'T': {0b11111, 0b00100, 0b00100, 0b00100, 0b00100, 0b00100, 0b00100},
	'U': {0b10001, 0b10001, 0b10001, 0b10001, 0b10001, 0b10001, 0b01110},
	'V': {0b10001, 0b10001, 0b10001, 0b10001, 0b10001, 0b01010, 0b00100},
	'W': {0b10001, 0b10001, 0b10001, 0b10101, 0b10101, 0b11011, 0b10001},
	'Y': {0b10001, 0b10001, 0b01010, 0b00100, 0b00100, 0b00100, 0b00100},
	'Z': {0b11111, 0b00001, 0b00010, 0b00100, 0b01000, 0b10000, 0b11111},
	'0': {0b01110, 0b10001, 0b10011, 0b10101, 0b11001, 0b10001, 0b01110},
	'1': {0b00100, 0b01100, 0b00100, 0b00100, 0b00100, 0b00100, 0b01110},
	'2': {0b01110, 0b10001, 0b00001, 0b00110, 0b01000, 0b10000, 0b11111},
	'3': {0b11111, 0b00010, 0b00100, 0b00010, 0b00001, 0b10001, 0b01110},
	'4': {0b00010, 0b00110, 0b01010, 0b10010, 0b11111, 0b00010, 0b00010},
	'5': {0b11111, 0b10000, 0b11110, 0b00001, 0b00001, 0b10001, 0b01110},
	'6': {0b00110, 0b01000, 0b10000, 0b11110, 0b10001, 0b10001, 0b01110},
	'7': {0b11111, 0b00001, 0b00010, 0b00100, 0b01000, 0b01000, 0b01000},
	'8': {0b01110, 0b10001, 0b10001, 0b01110, 0b10001, 0b10001, 0b01110},
	'9': {0b01110, 0b10001, 0b10001, 0b01111, 0b00001, 0b00010, 0b01100},
}

// GlyphFor — буква, которой помечается значок провайдера или блока карточки.
// Соответствие фиксированное, а не «первая буква названия»: иначе OPENROUTER
// и OPENCODE GO получили бы одну и ту же «O».
func GlyphFor(provType, label string) string {
	switch strings.ToLower(provType) {
	case "kimi":
		return "K"
	case "openrouter":
		return "R"
	case "opencode":
		return "O"
	case "codex":
		return "X"
	}
	// AGY — два блока в одной карточке: GEMINI и CLAUDE/GPT.
	up := strings.ToUpper(label)
	switch {
	case strings.HasPrefix(up, "GEMINI"):
		return "G"
	case strings.HasPrefix(up, "CLAUDE"):
		return "C"
	}
	for _, r := range up {
		if hasGlyph(r) {
			return string(r)
		}
	}
	return ""
}

func hasGlyph(r rune) bool {
	_, ok := glyphs[r]
	return ok
}

// GlyphSupported — есть ли такая буква в шрифте (для тестов и логов).
func GlyphSupported(g string) bool {
	rs := []rune(g)
	return len(rs) == 1 && hasGlyph(rs[0])
}

// drawGlyph рисует букву в отверстии кольца. Масштаб целочисленный и
// подбирается по отверстию, поэтому штрихи остаются пиксельными: 5×7 при
// 16 px, 10×14 при 32 px. Дробное масштабирование на таких размерах даёт мыло.
func drawGlyph(img *image.NRGBA, c, inner float64, glyph string, p Palette) {
	rs := []rune(strings.ToUpper(glyph))
	if len(rs) != 1 {
		return
	}
	pattern, ok := glyphs[rs[0]]
	if !ok {
		return
	}
	hole := inner*2 - 1 // диаметр отверстия без края кольца
	if hole < glyphH {
		return
	}
	scale := int(math.Floor(math.Min(hole/glyphW, hole/glyphH)))
	if scale < 1 {
		return
	}
	w, h := glyphW*scale, glyphH*scale
	left := int(math.Round(c - float64(w)/2))
	top := int(math.Round(c - float64(h)/2))
	alpha := float64(p.ArcA) / 255

	for gy := 0; gy < glyphH; gy++ {
		row := pattern[gy]
		for gx := 0; gx < glyphW; gx++ {
			if row&(1<<(glyphW-1-gx)) == 0 {
				continue
			}
			for dy := 0; dy < scale; dy++ {
				for dx := 0; dx < scale; dx++ {
					x, y := left+gx*scale+dx, top+gy*scale+dy
					if x < 0 || y < 0 || x >= img.Rect.Dx() || y >= img.Rect.Dy() {
						continue
					}
					// Буква живёт только в отверстии: заезжать на кольцо нельзя,
					// иначе она сливается с дугой.
					ddx := float64(x) + 0.5 - c
					ddy := float64(y) + 0.5 - c
					if math.Hypot(ddx, ddy) > inner-0.5 {
						continue
					}
					blendPixel(img, x, y, p.Arc, alpha)
				}
			}
		}
	}
}

// blendPixel кладёт цвет поверх пикселя с заданной непрозрачностью.
func blendPixel(img *image.NRGBA, x, y int, col RGB, a float64) {
	i := img.PixOffset(x, y)
	ia := float64(img.Pix[i+3]) / 255
	out := math.Min(1, a+ia)
	if out <= 0 {
		return
	}
	img.Pix[i+0] = mix(img.Pix[i+0], col.R, a, out)
	img.Pix[i+1] = mix(img.Pix[i+1], col.G, a, out)
	img.Pix[i+2] = mix(img.Pix[i+2], col.B, a, out)
	img.Pix[i+3] = uint8(math.Round(out * 255))
}
