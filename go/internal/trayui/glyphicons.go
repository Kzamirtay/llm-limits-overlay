package trayui

import (
	"bytes"
	"embed"
	"fmt"
	"image/png"
	"math"
	"strings"
	"sync"
)

// Иконки провайдеров: монохромные глифы из Simple Icons (CC0), растеризованы
// в PNG 64×64 и вложены в бинарник. Рисуются как маска альфы и тонируются в
// цвет интерфейса, поэтому одинаково выглядят на тёмной и светлой панели
// задач. Сами логотипы — товарные знаки соответствующих владельцев и здесь
// используются только как метки провайдеров.
//
//go:embed icons/*.png
var iconFS embed.FS

// iconAsset — соответствие типа провайдера файлу иконки. Пустое значение
// означает «иконки нет» — тогда остаётся буква (см. GlyphFor).
var iconAsset = map[string]string{
	"kimi":       "moonshotai",
	"openrouter": "openrouter",
	"opencode":   "opencode",
	"codex":      "", // OpenAI убрала логотип из Simple Icons — остаётся буква
}

// IconMask — монохромная маска: размер и альфа на пиксель.
type IconMask struct {
	Size  int
	Alpha []uint8
}

var (
	iconMu    sync.Mutex
	iconCache = map[string]*IconMask{}
)

// LoadIcon читает вложенную иконку по имени файла (без .png) и кэширует её.
func LoadIcon(name string) (*IconMask, error) {
	if name == "" {
		return nil, fmt.Errorf("trayui: empty icon name")
	}
	iconMu.Lock()
	defer iconMu.Unlock()
	if m, ok := iconCache[name]; ok {
		return m, nil
	}
	raw, err := iconFS.ReadFile("icons/" + name + ".png")
	if err != nil {
		return nil, fmt.Errorf("trayui: icon %q: %w", name, err)
	}
	img, err := png.Decode(bytes.NewReader(raw))
	if err != nil {
		return nil, fmt.Errorf("trayui: icon %q: %w", name, err)
	}
	b := img.Bounds()
	size := b.Dx()
	m := &IconMask{Size: size, Alpha: make([]uint8, size*size)}
	// Форма берётся из альфы: цвет внутри PNG не важен, тон задаётся при
	// отрисовке.
	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			_, _, _, a := img.At(b.Min.X+x, b.Min.Y+y).RGBA()
			m.Alpha[y*size+x] = uint8(a >> 8)
		}
	}
	iconCache[name] = m
	return m, nil
}

// IconFor — файл иконки для провайдера/блока карточки. Пустая строка — иконки
// нет (незнакомый провайдер или бренд без свободного логотипа).
func IconFor(provType, label string) string {
	if name, ok := iconAsset[strings.ToLower(provType)]; ok {
		return name
	}
	switch strings.ToLower(provType) {
	case "agy":
		up := strings.ToUpper(label)
		switch {
		case strings.HasPrefix(up, "GEMINI"):
			return "googlegemini"
		case strings.HasPrefix(up, "CLAUDE"):
			return "anthropic"
		}
	}
	return ""
}

// Scale пересэмплирует маску под нужный размер (билинейно): иконки нужны и в
// полосе (14-18 px), и в подсказке, и в значках трея.
func (m *IconMask) Scale(size int) *IconMask {
	if m == nil || size <= 0 {
		return &IconMask{}
	}
	if size == m.Size {
		return m
	}
	out := &IconMask{Size: size, Alpha: make([]uint8, size*size)}
	ratio := float64(m.Size) / float64(size)
	for y := 0; y < size; y++ {
		fy := (float64(y)+0.5)*ratio - 0.5
		y0 := int(math.Floor(fy))
		ty := fy - float64(y0)
		for x := 0; x < size; x++ {
			fx := (float64(x)+0.5)*ratio - 0.5
			x0 := int(math.Floor(fx))
			tx := fx - float64(x0)

			at := func(xx, yy int) float64 {
				if xx < 0 {
					xx = 0
				}
				if yy < 0 {
					yy = 0
				}
				if xx >= m.Size {
					xx = m.Size - 1
				}
				if yy >= m.Size {
					yy = m.Size - 1
				}
				return float64(m.Alpha[yy*m.Size+xx])
			}
			top := at(x0, y0)*(1-tx) + at(x0+1, y0)*tx
			bottom := at(x0, y0+1)*(1-tx) + at(x0+1, y0+1)*tx
			v := top*(1-ty) + bottom*ty
			out.Alpha[y*size+x] = uint8(math.Round(v))
		}
	}
	return out
}

// Coverage — доля закрашенных пикселей: помогает в тестах отличить «иконка
// загрузилась» от «пустая маска».
func (m *IconMask) Coverage() float64 {
	if m == nil || len(m.Alpha) == 0 {
		return 0
	}
	var sum uint64
	for _, a := range m.Alpha {
		sum += uint64(a)
	}
	return float64(sum) / float64(len(m.Alpha)) / 255
}
