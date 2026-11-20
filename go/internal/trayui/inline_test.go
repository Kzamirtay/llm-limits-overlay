package trayui

import (
	"strings"
	"testing"

	"llmlimits/internal/config"
	"llmlimits/internal/model"
)

func inlineSummary() Summary {
	return Summary{State: StateOK, Items: []Item{
		{Label: "KIMI", Glyph: "K", Worst: 5.7, State: StateOK, Values: []Value{{Label: "7д", Pct: "6%"}}},
		{Label: "OPENCODE GO", Glyph: "O", Worst: 19, State: StateOK, Values: []Value{{Label: "7д", Pct: "19%"}}},
		{Label: "GEMINI", Glyph: "G", Worst: 0, State: StateOK, Values: []Value{{Label: "7д", Pct: "0%"}}},
	}}
}

// Строка полосы: буква, процент, два пробела между провайдерами.
func TestInlineSegments_Text(t *testing.T) {
	segs := InlineSegments(inlineSummary())
	if got, want := InlineText(segs), "K 6%   O 19%   G 0%"; got != want {
		t.Fatalf("inline text = %q, want %q", got, want)
	}
	if len(segs) != 5 { // 3 куска + 2 разделителя
		t.Fatalf("segments = %d, want 5", len(segs))
	}
	for i := 0; i < len(segs); i += 2 {
		if segs[i].Style != InlineNormal {
			t.Errorf("segment %d style = %v, want normal", i, segs[i].Style)
		}
	}
}

// Нет данных — «--» приглушённо; исчерпано — жирным.
func TestInlineSegments_Styles(t *testing.T) {
	sum := Summary{State: StateOK, Items: []Item{
		{Label: "KIMI", Glyph: "K", State: StateUnknown},
		{Label: "OPENCODE GO", Glyph: "O", Worst: 100, State: StateOK},
	}}
	segs := InlineSegments(sum)
	if segs[0].Text != "K --" || segs[0].Style != InlineDim {
		t.Errorf("no-data segment = %+v", segs[0])
	}
	if segs[2].Text != "O 100%" || segs[2].Style != InlineBold {
		t.Errorf("exhausted segment = %+v", segs[2])
	}
}

// Пустая сводка не даёт ни строки, ни висящих разделителей.
func TestInlineSegments_Empty(t *testing.T) {
	if got := InlineText(InlineSegments(Summary{})); got != "" {
		t.Errorf("empty summary = %q, want empty line", got)
	}
}

// Если иконки у провайдера нет, в строке остаётся буква.
func TestInlineSegments_LetterFallback(t *testing.T) {
	sum := Summary{State: StateOK, Items: []Item{
		{Label: "CODEX", Glyph: "X", Worst: 5, State: StateOK},
	}}
	segs := InlineSegments(sum)
	if segs[0].Icon != "" || segs[0].Text != "X 5%" {
		t.Errorf("segment = %+v, want letter fallback", segs[0])
	}
}

// Провайдер без назначенной буквы всё равно получает метку.
func TestInlineSegments_FallbackGlyph(t *testing.T) {
	sum := Summary{State: StateOK, Items: []Item{{Label: "MYTHING", Worst: 4, State: StateOK}}}
	if got := InlineText(InlineSegments(sum)); got != "M 4%" {
		t.Errorf("fallback = %q, want M 4%%", got)
	}
	sum.Items[0].Label = "свой"
	if got := InlineText(InlineSegments(sum)); got != "• 4%" {
		t.Errorf("no-letter fallback = %q", got)
	}
}

// Ключ меняется вместе со строкой — по нему решается, перерисовывать ли полосу.
func TestInlineKey_ChangesWithData(t *testing.T) {
	a := InlineKey(InlineSegments(inlineSummary()))
	sum := inlineSummary()
	sum.Items[1].Worst = 24
	b := InlineKey(InlineSegments(sum))
	if a == b {
		t.Fatal("key must change when a percentage changes")
	}
	if a != InlineKey(InlineSegments(inlineSummary())) {
		t.Error("key must be stable for identical data")
	}
}

// Строка полосы согласуется со сводкой из реального снапшота.
func TestInlineSegments_FromSnapshot(t *testing.T) {
	// CLAUDE выключен настройкой, как в реальном конфиге проекта: полоса не
	// должна показывать скрытый блок.
	cfg := &config.Config{
		Providers: []config.Provider{prov("kimi", "kimi", "KIMI"), prov("agy", "agy", "GEMINI + CLAUDE/GPT")},
		Limits:    map[string]map[string]bool{"agy": {"claude": false}},
	}
	snap := &model.Snapshot{
		AgyStatus:    "online",
		GeminiWeekly: bkt(47, false), Gemini5h: bkt(88, false),
		Providers: []*model.ProviderState{{
			Id: "kimi", Status: "online",
			Kimi: &model.KimiInfo{Limit7d: bkt(6, false), Limit5h: bkt(0, false)},
		}},
	}
	sum := BuildSummary(snap, cfg)
	segs := InlineSegments(sum)
	// У обоих провайдеров есть иконка, поэтому буквы в строке не нужны.
	if got, want := InlineText(segs), "6%   88%"; got != want {
		t.Errorf("inline = %q, want %q", got, want)
	}
	if segs[0].Icon != "moonshotai" || segs[2].Icon != "googlegemini" {
		t.Errorf("icons = %q, %q", segs[0].Icon, segs[2].Icon)
	}
	if strings.Contains(InlineText(segs), "—") {
		t.Error("inline line must not contain menu dashes")
	}
}
