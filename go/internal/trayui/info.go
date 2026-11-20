package trayui

import (
	"fmt"
	"strings"
	"unicode/utf16"

	"llmlimits/internal/config"
	"llmlimits/internal/layout"
	"llmlimits/internal/model"
)

// Value — окно провайдера в сводке: подпись окна, процент и остаток до
// сброса. ResetIn остаётся в исходном виде ("3h 19m"), форматирование —
// задача слоя отображения.
type Value struct {
	Label string // "7д", "5ч", "free", "лимит"
	Pct   string // "9%", "100%", "--"
	Reset string // "3h 19m", "" если данных нет
}

// String — значение без остатка: "7д 9%" (для меню и подсказки значков).
func (v Value) String() string {
	if v.Label == "" {
		return v.Pct
	}
	return v.Label + " " + v.Pct
}

// Item — одна строка сводки: провайдер или блок карточки AGY. На каждый Item
// в трее приходится отдельный значок.
type Item struct {
	// Label — полное имя как в карточке оверлея ("OPENCODE GO"), Short —
	// короткое для тултипа, где каждый символ на счету ("OPENCODE").
	Label string
	Short string
	// Values — окна этого провайдера в порядке карточки.
	Values []Value
	// Note — состояние вместо чисел: "нет ключа", "stale", "ошибка".
	Note string
	// Glyph — буква в отверстии кольца (K/O/G/R/X/C): единственный способ
	// отличить значки на 16 px, где подпись не читается.
	Glyph string
	// Icon — имя вложенной иконки провайдера (см. glyphicons.go). Пустое —
	// иконки нет, рисуется буква.
	Icon string
	// Worst — худший расход среди окон этого значка, State — есть ли данные.
	Worst float64
	State State
}

// MenuTitle — строка для контекстного меню трея: меню рисует настоящий шрифт
// в настоящем DPI, поэтому здесь можно позволить полное имя и разделители.
func (it Item) MenuTitle() string {
	parts := []string{it.Label + " —"}
	switch {
	case len(it.Values) > 0:
		parts = append(parts, strings.Join(valueStrings(it.Values), " · "))
	case it.Note != "":
		parts = append(parts, it.Note)
	default:
		parts = append(parts, "нет данных")
	}
	if it.Note != "" && len(it.Values) > 0 {
		parts = append(parts, "("+it.Note+")")
	}
	return strings.Join(parts, " ")
}

// TooltipPart — кусок однострочной сводки: "KIMI 7д 100% 5ч 39%".
func (it Item) TooltipPart() string {
	name := it.Short
	if name == "" {
		name = it.Label
	}
	if len(it.Values) == 0 {
		if it.Note != "" {
			return name + " " + it.Note
		}
		return name + " --"
	}
	return name + " " + strings.Join(valueStrings(it.Values), " ")
}

// IconTip — подсказка отдельного значка провайдера.
func IconTip(it Item) string {
	return fitTip(strings.ReplaceAll(it.MenuTitle(), " — ", " "))
}

// Summary — итог одного цикла фетча для трея.
type Summary struct {
	// Items — строки меню/значков в порядке карточек оверлея.
	Items []Item
	// WorstPercent — худший расход среди всех значков.
	WorstPercent float64
	// State — есть ли вообще измеренные данные.
	State State
}

// tooltipLimit — предел NOTIFYICONDATA.szTip: 128 UTF-16 юнитов вместе с
// терминатором, дальше оболочка молча обрезает строку.
const tooltipLimit = 127

// BuildSummary собирает сводку по тем же правилам, что и окно оверлея:
// берутся только включённые провайдеры и только показанные окна
// (layout.CardHeight/rows), поэтому трей не показывает того, чего нет на HUD.
func BuildSummary(snap *model.Snapshot, cfg *config.Config) Summary {
	sum := Summary{State: StateUnknown}
	if cfg == nil || snap == nil {
		// До первого фетча показывать нечего: пустой значок без дуги
		// честнее выдуманного «0 %».
		return sum
	}
	for _, p := range cfg.EffectiveProviders() {
		if !p.Enabled {
			continue
		}
		st := providerState(snap, p.Id)
		title := p.Title
		if title == "" {
			title = strings.ToUpper(p.Id)
		}
		status := "online"
		if st != nil {
			status = st.Status
		}
		// Пометить строку устаревшей данными: сам процент при этом остаётся
		// от последнего успешного фетча — как на карточке оверлея.
		mark := func(it Item) Item {
			if status == "stale" {
				it.Note = noteFor(status)
			}
			return it
		}

		switch p.Type {
		case "kimi":
			rows := layout.KimiRowList(cfg, p, st)
			if len(rows) == 0 {
				continue
			}
			it := Item{
				Label: title, Short: shortLabel(p.Type, title),
				Glyph: GlyphFor(p.Type, title), Icon: IconFor(p.Type, title),
			}
			it.observe(status)
			if unusable(status) {
				it.Note = noteFor(status)
			} else {
				it.addRows(rows)
			}
			sum.Items = append(sum.Items, mark(it))

		case "openrouter":
			var or *model.OpenRouterInfo
			if st != nil {
				or = st.OpenRouter
			}
			it := Item{
				Label: title, Short: shortLabel(p.Type, title),
				Glyph: GlyphFor(p.Type, title), Icon: IconFor(p.Type, title),
			}
			it.observe(status)
			if unusable(status) || or == nil {
				it.Note = noteFor(status)
				sum.Items = append(sum.Items, mark(it))
				continue
			}
			if layout.OpenRouterShowLimit(cfg, p) {
				it.addValue("лимит", or.UsedPercent, false, or.ResetIn)
			}
			if layout.OpenRouterShowFree(cfg, p, st) {
				if or.FreeDailyLimit > 0 {
					freeFrac := float64(or.FreeDailyUsed) / float64(or.FreeDailyLimit)
					if freeFrac > 1 {
						freeFrac = 1
					}
					if freeFrac*100 > it.Worst {
						it.Worst = freeFrac * 100
					}
				}
				it.Values = append(it.Values, Value{
					Label: "free",
					Pct:   fmt.Sprintf("%d/%d", or.FreeDailyUsed, or.FreeDailyLimit),
				})
			}
			sum.Items = append(sum.Items, mark(it))

		case "opencode":
			wins := layout.OpenCodeWindows(cfg, p, st)
			if len(wins) == 0 {
				continue
			}
			it := Item{
				Label: title, Short: shortLabel(p.Type, title),
				Glyph: GlyphFor(p.Type, title), Icon: IconFor(p.Type, title),
			}
			it.observe(status)
			if unusable(status) {
				it.Note = noteFor(status)
			} else {
				for _, w := range wins {
					it.addValue(windowLabel(w.LimitKey()), w.UsedPercent, w.Placeholder, w.ResetIn)
				}
			}
			sum.Items = append(sum.Items, mark(it))

		case "codex":
			rows := layout.CodexRowList(cfg, p, st)
			if len(rows) == 0 {
				continue
			}
			it := Item{
				Label: title, Short: shortLabel(p.Type, title),
				Glyph: GlyphFor(p.Type, title), Icon: IconFor(p.Type, title),
			}
			it.observe(status)
			if unusable(status) {
				it.Note = noteFor(status)
			} else {
				it.addRows(rows)
			}
			sum.Items = append(sum.Items, mark(it))

		case "agy":
			agyStatus := status
			if snap != nil && snap.AgyStatus != "" {
				agyStatus = snap.AgyStatus
			}
			geminiOn, claudeOn := layout.AgyShowGroups(cfg, p)
			var gw, g5, cw, c5 model.Bucket
			if snap != nil {
				gw, g5, cw, c5 = snap.GeminiWeekly, snap.Gemini5h, snap.ClaudeWeekly, snap.Claude5h
			}
			groups := []struct {
				label string
				short string
				rows  []layout.Row
			}{
				{"GEMINI", "GEM", layout.AgyRowList(geminiOn, gw, g5)},
				{"CLAUDE/GPT", "CLAUDE", layout.AgyRowList(claudeOn, cw, c5)},
			}
			for _, g := range groups {
				if len(g.rows) == 0 {
					continue
				}
				it := Item{
					Label: g.label, Short: g.short,
					Glyph: GlyphFor(p.Type, g.label), Icon: IconFor(p.Type, g.label),
				}
				it.observe(agyStatus)
				if unusable(agyStatus) {
					it.Note = noteFor(agyStatus)
				} else {
					it.addRows(g.rows)
					if agyStatus == "stale" {
						it.Note = noteFor(agyStatus)
					}
				}
				sum.Items = append(sum.Items, it)
			}
		}
	}

	for _, it := range sum.Items {
		if it.State == StateOK {
			sum.State = StateOK
		}
		if it.Worst > sum.WorstPercent {
			sum.WorstPercent = it.Worst
		}
	}
	return sum
}

// observe отмечает, что данные вообще есть: значок с дугой рисуем, только
// если провайдер ответил измеренными числами.
func (it *Item) observe(status string) {
	if !unusable(status) {
		it.State = StateOK
	}
}

// addValue добавляет окно в строку и обновляет худший процент значка.
// resetIn сохраняется как есть (например "3h 19m"): перевод единиц — дело
// отображения, сводка не должна зависеть от языка интерфейса.
func (it *Item) addValue(label string, pct float64, placeholder bool, resetIn string) {
	if placeholder {
		it.Values = append(it.Values, Value{Label: label, Pct: "--"})
		return
	}
	it.Values = append(it.Values, Value{
		Label: label,
		Pct:   fmt.Sprintf("%.0f%%", pct),
		Reset: resetIn,
	})
	if pct > it.Worst {
		it.Worst = pct
	}
}

// valueStrings — значения без остатка до сброса: для меню и подсказки значков
// трея, где каждый символ на счету.
func valueStrings(values []Value) []string {
	out := make([]string, len(values))
	for i, v := range values {
		out[i] = v.String()
	}
	return out
}

// addRows — то же для набора layout.Row.
func (it *Item) addRows(rows []layout.Row) {
	for _, r := range rows {
		it.addValue(windowLabel(r.Key), r.Bucket.UsedPercent, r.Bucket.Placeholder, r.Bucket.ResetIn)
	}
}

// windowLabel переводит ключ настройки в подпись окна как на карточке.
func windowLabel(key string) string {
	switch key {
	case "7d":
		return "7д"
	case "5h":
		return "5ч"
	case "30d":
		return "30д"
	case "primary":
		return "осн"
	case "secondary":
		return "доп"
	case "limit":
		return "лимит"
	case "free":
		return "free"
	}
	return key
}

// shortLabel — имя покороче для тултипа (там всего 127 символов на всё).
func shortLabel(provType, title string) string {
	switch provType {
	case "openrouter":
		return "OPENROUTER"
	case "opencode":
		return "OPENCODE"
	}
	return title
}

// unusable — провайдер не дал измеренных данных: процент рисовать нельзя.
func unusable(status string) bool {
	switch status {
	case "no_key", "error", "":
		return true
	}
	return false
}

func noteFor(status string) string {
	switch status {
	case "no_key":
		return "нет ключа"
	case "error":
		return "ошибка"
	case "stale":
		return "stale"
	}
	return ""
}

func providerState(snap *model.Snapshot, id string) *model.ProviderState {
	if snap == nil {
		return nil
	}
	return snap.Find(id)
}

// fitTip обрезает строку по лимиту NOTIFYICONDATA.szTip. Хвост, который не
// влез, отбрасывается с многоточием: молча обрезанная оболочкой строка
// выглядит как «сломанный» тултип, а явное «…» честно говорит, что не влезло.
func fitTip(s string) string {
	if utf16Len(s) <= tooltipLimit {
		return s
	}
	ellipsis := "…"
	limit := tooltipLimit - utf16Len(ellipsis)
	var b strings.Builder
	used := 0
	for _, r := range s {
		n := utf16.RuneLen(r)
		if used+n > limit {
			break
		}
		b.WriteRune(r)
		used += n
	}
	return strings.TrimRight(b.String(), " ·") + ellipsis
}

func utf16Len(s string) int {
	n := 0
	for _, r := range s {
		n += utf16.RuneLen(r)
	}
	return n
}
