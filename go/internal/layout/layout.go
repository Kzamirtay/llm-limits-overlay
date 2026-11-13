// Package layout — политика отображения окон: какие строки рисовать
// (настройка limits + правило «исчерпанное длинное окно отменяет короткие»)
// и сколько места занимает карточка. Одна и та же логика используется
// GUI (cmd/overlay32) и CLI (cmd/llm-limits), чтобы высота окна и строки
// не разъезжались.
package layout

import (
	"llmlimits/internal/config"
	"llmlimits/internal/model"
)

// Show — включено ли окно key у провайдера (overlay.config.json -> "limits").
func Show(cfg *config.Config, p config.Provider, key string) bool {
	return cfg.ShowWindow(p.Type, key)
}

// Exhausted — окно израсходовано по измеренным данным. Заглушка (нет данных)
// исчерпанной не считается: неизвестность не должна схлопывать карточку.
func Exhausted(b model.Bucket) bool {
	if b.Placeholder {
		return false
	}
	return b.UsedFraction >= 1.0 || b.UsedPercent >= 100.0
}

// Row — строка карточки с ключом окна (нужен для подписей в CLI).
type Row struct {
	Bucket model.Bucket
	Key    string
}

// truncate отбрасывает окна после первого исчерпанного. rows обязаны идти
// от длинного окна к короткому: если недельный лимит выбран, часовой уже
// не имеет смысла.
func truncate(rows []Row) []Row {
	for i, r := range rows {
		if Exhausted(r.Bucket) {
			return rows[:i+1]
		}
	}
	return rows
}

func buckets(rows []Row) []model.Bucket {
	out := make([]model.Bucket, len(rows))
	for i := range rows {
		out[i] = rows[i].Bucket
	}
	return out
}

// KimiRowList — строки KIMI: 7d сверху, 5h снизу.
func KimiRowList(cfg *config.Config, p config.Provider, st *model.ProviderState) []Row {
	var k7d, k5h model.Bucket
	if st != nil && st.Kimi != nil {
		k7d = st.Kimi.Limit7d
		k5h = st.Kimi.Limit5h
	}
	rows := []Row{}
	if Show(cfg, p, "7d") {
		rows = append(rows, Row{k7d, "7d"})
	}
	if Show(cfg, p, "5h") {
		rows = append(rows, Row{k5h, "5h"})
	}
	return truncate(rows)
}

// KimiRows — только бакеты (для GUI).
func KimiRows(cfg *config.Config, p config.Provider, st *model.ProviderState) []model.Bucket {
	return buckets(KimiRowList(cfg, p, st))
}

// OpenCodeWindows — окна OpenCode Go в порядке 30д → 7д → 5ч.
// Окно, помеченное сервером как rate-limited, считается исчерпанным даже
// при проценте ниже 100.
func OpenCodeWindows(cfg *config.Config, p config.Provider, st *model.ProviderState) []model.Window {
	var oc *model.OpenCodeInfo
	if st != nil {
		oc = st.OpenCode
	}
	if oc == nil {
		return nil
	}
	out := []model.Window{}
	for _, w := range oc.Windows() {
		if Show(cfg, p, w.LimitKey()) {
			out = append(out, w)
		}
	}
	for i, w := range out {
		if w.Limited || Exhausted(w.Bucket) {
			return out[:i+1]
		}
	}
	return out
}

// CodexRowList — primary/secondary в порядке карточки. Если более длинное окно
// исчерпано, короткое не показываем (длину определяем по window_mins).
func CodexRowList(cfg *config.Config, p config.Provider, st *model.ProviderState) []Row {
	var prim model.Bucket
	var primMin int
	var sec *model.Bucket
	var secMin int
	if st != nil && st.Codex != nil {
		prim = st.Codex.Primary
		primMin = st.Codex.PrimaryMin
		sec = st.Codex.Secondary
		secMin = st.Codex.SecondaryMin
	}

	type entry struct {
		row  Row
		mins int
	}
	rows := []entry{}
	if Show(cfg, p, "primary") {
		rows = append(rows, entry{Row{prim, "primary"}, primMin})
	}
	if sec != nil && Show(cfg, p, "secondary") {
		rows = append(rows, entry{Row{*sec, "secondary"}, secMin})
	}
	if len(rows) == 0 {
		return nil
	}

	longest := 0
	for i := range rows {
		if rows[i].mins > rows[longest].mins {
			longest = i
		}
	}
	if len(rows) > 1 && Exhausted(rows[longest].row.Bucket) {
		return []Row{rows[longest].row}
	}

	out := make([]Row, len(rows))
	for i := range rows {
		out[i] = rows[i].row
	}
	return out
}

// CodexRows — только бакеты (для GUI).
func CodexRows(cfg *config.Config, p config.Provider, st *model.ProviderState) []model.Bucket {
	return buckets(CodexRowList(cfg, p, st))
}

// OpenRouterShowLimit — показывать ли строку бюджета ключа.
func OpenRouterShowLimit(cfg *config.Config, p config.Provider) bool {
	return Show(cfg, p, "limit")
}

// OpenRouterShowFree — показывать ли строку free-пула (нужен непустой лимит).
func OpenRouterShowFree(cfg *config.Config, p config.Provider, st *model.ProviderState) bool {
	if !Show(cfg, p, "free") {
		return false
	}
	if st != nil && st.OpenRouter != nil {
		return st.OpenRouter.FreeDailyLimit > 0
	}
	return true // пока данных нет, считаем free-пул доступным (как раньше)
}

// Snapshot‑независимые группы agy.

// AgyShowGroups — какие блоки карточки AGY включены настройкой.
func AgyShowGroups(cfg *config.Config, p config.Provider) (gemini, claude bool) {
	return Show(cfg, p, "gemini"), Show(cfg, p, "claude")
}

// AgyRowList — строки одного блока (weekly сверху, 5h снизу) с тем же правилом
// «исчерпан длинный — короткий не нужен».
func AgyRowList(enabled bool, weekly, five model.Bucket) []Row {
	if !enabled {
		return nil
	}
	return truncate([]Row{{weekly, "7d"}, {five, "5h"}})
}

// AgyRows — только бакеты (для GUI).
func AgyRows(enabled bool, weekly, five model.Bucket) []model.Bucket {
	return buckets(AgyRowList(enabled, weekly, five))
}

// ---- высоты карточек ----

// KimiHeight — 2 строки = 94 px, 1 строка = 64 px.
func KimiHeight(n int) float32 {
	if n <= 0 {
		return 0
	}
	return 34 + 30*float32(n)
}

// OpenCodeHeight — n строк с шагом 28 px.
func OpenCodeHeight(n int) float32 {
	if n <= 0 {
		return 0
	}
	return 40 + 28*float32(n)
}

// CodexHeight — 1 строка = 62 px, каждая следующая +32 px.
func CodexHeight(n int) float32 {
	if n <= 0 {
		return 0
	}
	return 62 + 32*float32(n-1)
}

// OpenRouterHeight — две строки (бюджет + free) = 91 px, одна = 62 px.
func OpenRouterHeight(showLimit, showFree bool) float32 {
	switch {
	case showLimit && showFree:
		return 91
	case showLimit || showFree:
		return 62
	}
	return 0
}

// AgyGroupHeight — блок GEMINI/CLAUDE: заголовок + n строк.
func AgyGroupHeight(n int) float32 {
	if n <= 0 {
		return 0
	}
	return 59 + 30*float32(n-1)
}

// AgyHeight — высота карточки AGY: блоки + разделитель 8 px между ними.
func AgyHeight(geminiRows, claudeRows int) float32 {
	h := AgyGroupHeight(geminiRows) + AgyGroupHeight(claudeRows)
	if geminiRows > 0 && claudeRows > 0 {
		h += 8
	}
	return h
}

// CardHeight — высота карточки провайдера с учётом настроек и исчерпания.
// 0 означает «карточку не рисовать».
func CardHeight(cfg *config.Config, p config.Provider, st *model.ProviderState, snap *model.Snapshot) float32 {
	switch p.Type {
	case "kimi":
		return KimiHeight(len(KimiRows(cfg, p, st)))
	case "openrouter":
		return OpenRouterHeight(OpenRouterShowLimit(cfg, p), OpenRouterShowFree(cfg, p, st))
	case "opencode":
		return OpenCodeHeight(len(OpenCodeWindows(cfg, p, st)))
	case "codex":
		return CodexHeight(len(CodexRows(cfg, p, st)))
	case "agy":
		gemini, claude := AgyShowGroups(cfg, p)
		var gw, g5, cw, c5 model.Bucket
		if snap != nil {
			gw, g5, cw, c5 = snap.GeminiWeekly, snap.Gemini5h, snap.ClaudeWeekly, snap.Claude5h
		}
		return AgyHeight(len(AgyRows(gemini, gw, g5)), len(AgyRows(claude, cw, c5)))
	}
	return 0
}
