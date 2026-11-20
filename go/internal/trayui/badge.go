// Package trayui — содержимое значка в системном трее: кольцо-индикатор
// израсходованной квоты, компактная сводка для тултипа и строки меню.
// Пакет намеренно не тянет Windows API: иконку и строки можно проверить
// юнит-тестами на любой ОС, а платформенная часть (Shell_NotifyIcon,
// реестр, .ico) живёт в cmd/overlay32.
package trayui

import (
	"image"
	"math"
)

// RGB — цвет без альфы.
type RGB struct{ R, G, B uint8 }

// Palette — цвета кольца. Значок рисуется в панели задач, которая бывает и
// светлой, и тёмной, поэтому цвета задаются снаружи (см. cmd/overlay32).
type Palette struct {
	Track  RGB // «пустая» часть кольца
	Arc    RGB // израсходованная часть
	TrackA uint8
	ArcA   uint8
}

// State — что вообще известно о квоте.
type State int

const (
	// StateUnknown — нулевое значение: данных нет (нет ключа/ошибка),
	// рисуется только трек, без дуги. Именно поэтому «нет данных» идёт
	// первым: пустая структура обязана означать неизвестность, а не 0 %.
	StateUnknown State = iota
	// StateOK — есть измеренные данные, дуга рисуется по проценту.
	StateOK
)

// supersampling — 4×4 подвыборок на пиксель: кольцо в 16 px иначе выглядит
// рваным, а сглаживание GDI+ тут недоступно (иконку собираем пикселями).
const supersampling = 4

// Badge рисует квадратное кольцо size×size, заполненное на fraction (0..1)
// по часовой стрелке от 12 часов. Центр прозрачный: значок не превращается
// в залитый круг и остаётся читаемым на любом фоне. mark добавляет метку
// в центре — по ней различаются значки разных провайдеров.
func Badge(size int, fraction float64, st State, p Palette, glyph string) *image.NRGBA {
	if size < 8 {
		size = 8
	}
	img := image.NewNRGBA(image.Rect(0, 0, size, size))

	c := float64(size) / 2
	outer := c - 0.5
	// Толщина кольца: 17% размера, но не тоньше 1.5 px — иначе на 16 px
	// антиалиасинг съедает контраст. Меньше 17% не даёт выигрыша: отверстие
	// нужно ещё и букве (5×7 px при значке 16 px).
	thick := math.Max(1.5, float64(size)*0.17)
	inner := outer - thick
	if inner < 0 {
		inner = 0
	}

	frac := fraction
	if math.IsNaN(frac) || frac < 0 {
		frac = 0
	}
	if frac > 1 {
		frac = 1
	}
	arcEnd := frac * 2 * math.Pi

	trackA := float64(p.TrackA) / 255
	arcA := float64(p.ArcA) / 255
	steps := float64(supersampling * supersampling)

	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			var aTrack, aArc float64
			for sy := 0; sy < supersampling; sy++ {
				for sx := 0; sx < supersampling; sx++ {
					px := float64(x) + (float64(sx)+0.5)/supersampling
					py := float64(y) + (float64(sy)+0.5)/supersampling
					dx, dy := px-c, py-c
					r := math.Hypot(dx, dy)
					if r > outer || r < inner {
						continue
					}
					if st == StateOK && arcEnd > 0 && angleFromTop(dx, dy) <= arcEnd {
						aArc++
					} else {
						aTrack++
					}
				}
			}
			if aTrack == 0 && aArc == 0 {
				continue
			}

			// Обе части кольца разнесены по углу, поэтому покрытия
			// складываются без наложения: взвешенное среднее по цвету.
			wt := aTrack / steps * trackA
			wa := aArc / steps * arcA
			a := wt + wa
			if a <= 0 {
				continue
			}
			img.Pix[y*img.Stride+x*4+0] = lerp(p.Track.R, p.Arc.R, wa, a)
			img.Pix[y*img.Stride+x*4+1] = lerp(p.Track.G, p.Arc.G, wa, a)
			img.Pix[y*img.Stride+x*4+2] = lerp(p.Track.B, p.Arc.B, wa, a)
			img.Pix[y*img.Stride+x*4+3] = uint8(math.Round(math.Min(a, 1) * 255))
		}
	}

	drawGlyph(img, c, inner, glyph, p)
	return img
}

// mix накладывает цвет поверх уже накопленного с непрозрачностью out.
func mix(old uint8, add uint8, a, out float64) uint8 {
	v := (float64(old)*(out-a) + float64(add)*a) / out
	if v < 0 {
		v = 0
	}
	if v > 255 {
		v = 255
	}
	return uint8(math.Round(v))
}

// angleFromTop — угол против часовой стрелки от 12 часов в диапазоне [0, 2π):
// 12 часов = 0, 3 часа = π/2, 6 часов = π, 9 часов = 3π/2.
func angleFromTop(dx, dy float64) float64 {
	a := math.Atan2(dx, -dy)
	if a < 0 {
		a += 2 * math.Pi
	}
	return a
}

// lerp смешивает компоненту: wa — вклад дуги в суммарную непрозрачность a.
func lerp(track, arc uint8, wa, a float64) uint8 {
	v := (float64(track)*(a-wa) + float64(arc)*wa) / a
	if v < 0 {
		v = 0
	}
	if v > 255 {
		v = 255
	}
	return uint8(math.Round(v))
}

// BadgeSizes — размеры, которые панель задач Windows запрашивает при разном
// DPI: 16 px (100%), 20 px (125%), 24 px (150%), 32 px (200%), 40 px (250%).
// Все они кладутся в один .ico, чтобы оболочка брала точный размер, а не
// растягивала один и тот же.
var BadgeSizes = []int{16, 20, 24, 32, 40}

// QuantizePercent сводит процент к шагу, чтобы значок не пересобирался на
// каждое дробное изменение: systray пишет каждый уникальный образ в отдельный
// файл %TEMP%, и «1% шаг» означал бы сотню файлов вместо двух десятков.
func QuantizePercent(pct float64, step float64) float64 {
	if step <= 0 {
		return pct
	}
	if math.IsNaN(pct) || pct < 0 {
		return 0
	}
	if pct > 100 {
		pct = 100
	}
	return math.Round(pct/step) * step
}
