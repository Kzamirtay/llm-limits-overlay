package trayui

import (
	"fmt"
	"strings"
)

// InlineStyle — как рисовать кусок строки на панели задач. Цвет остаётся
// монохромным (как весь оверлей): различаются только яркость и начертание.
type InlineStyle int

const (
	// InlineNormal — обычные измеренные данные.
	InlineNormal InlineStyle = iota
	// InlineDim — данных нет или они устарели: рисовать приглушённо.
	InlineDim
	// InlineBold — лимит исчерпан (100% и больше).
	InlineBold
)

// InlineSegment — кусок однострочной сводки на панели задач. Если Icon задан,
// сначала рисуется иконка провайдера, а Text содержит только процент.
type InlineSegment struct {
	Text  string
	Style InlineStyle
	// Icon — имя вложенной иконки (см. glyphicons.go); пусто — иконки нет.
	Icon string
}

// InlineSegments собирает строку вида «K 6%  O 19%  G 0%» для полосы слева
// от области уведомлений: буква провайдера и его худший процент — то же
// число, что заполняет кольцо значка, но текстом, который читается на любом
// DPI.
func InlineSegments(sum Summary) []InlineSegment {
	segs := make([]InlineSegment, 0, len(sum.Items)*2)
	for _, it := range sum.Items {
		if segs != nil && len(segs) > 0 {
			segs = append(segs, InlineSegment{Text: "   ", Style: InlineDim})
		}
		segs = append(segs, inlineSegment(it))
	}
	return segs
}

// inlineSegment — один провайдер: иконка (или буква), процент и стиль.
func inlineSegment(it Item) InlineSegment {
	seg := InlineSegment{Icon: it.Icon}
	letter := it.Glyph
	if letter == "" {
		letter = fallbackGlyph(it.Label)
	}
	if it.State != StateOK {
		seg.Style = InlineDim
		seg.Text = "--"
		if seg.Icon == "" {
			seg.Text = letter + " --"
		}
		return seg
	}
	if it.Worst >= 100 {
		seg.Style = InlineBold
	}
	seg.Text = fmt.Sprintf("%.0f%%", it.Worst)
	if seg.Icon == "" {
		seg.Text = fmt.Sprintf("%s %.0f%%", letter, it.Worst)
	}
	return seg
}

// fallbackGlyph — буква для провайдера, которому она не назначена явно.
func fallbackGlyph(label string) string {
	for _, r := range strings.ToUpper(label) {
		if hasGlyph(r) {
			return string(r)
		}
	}
	return "•"
}

// InlineText — та же строка без стилей (для логов, тестов и подсказки).
func InlineText(segs []InlineSegment) string {
	var b strings.Builder
	for _, s := range segs {
		b.WriteString(s.Text)
	}
	return b.String()
}

// InlineKey — компактный ключ строки: по нему видно, нужно ли перерисовывать
// полосу и пересчитывать её ширину.
func InlineKey(segs []InlineSegment) string {
	var b strings.Builder
	for _, s := range segs {
		fmt.Fprintf(&b, "%d:%s:%s|", int(s.Style), s.Icon, s.Text)
	}
	return b.String()
}
