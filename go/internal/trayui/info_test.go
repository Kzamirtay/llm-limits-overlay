package trayui

import (
	"strings"
	"testing"

	"llmlimits/internal/config"
	"llmlimits/internal/model"
)

func bkt(pct float64, placeholder bool) model.Bucket {
	return model.Bucket{UsedPercent: pct, UsedFraction: pct / 100, ResetIn: "1d 2h", Placeholder: placeholder}
}

func prov(id, typ, title string) config.Provider {
	return config.Provider{Id: id, Type: typ, Title: title, Enabled: true}
}

func TestBuildSummary_Kimi(t *testing.T) {
	cfg := &config.Config{Providers: []config.Provider{prov("kimi", "kimi", "KIMI")}}
	snap := &model.Snapshot{Providers: []*model.ProviderState{{
		Id: "kimi", Status: "online",
		Kimi: &model.KimiInfo{Limit7d: bkt(100, false), Limit5h: bkt(11.3, false)},
	}}}

	sum := BuildSummary(snap, cfg)
	if len(sum.Items) != 1 {
		t.Fatalf("items = %d, want 1", len(sum.Items))
	}
	// Исчерпанный недельный лимит скрывает часовой — как на карточке.
	if got := sum.Items[0].MenuTitle(); got != "KIMI — 7д 100%" {
		t.Errorf("menu title = %q", got)
	}
	if sum.WorstPercent != 100 || sum.State != StateOK {
		t.Errorf("worst = %v state = %v", sum.WorstPercent, sum.State)
	}
	if got := sum.Items[0].TooltipPart(); got != "KIMI 7д 100%" {
		t.Errorf("tooltip part = %q", got)
	}
}

func TestBuildSummary_OpenCodeAndAgy(t *testing.T) {
	cfg := &config.Config{Providers: []config.Provider{
		prov("opencode", "opencode", "OPENCODE GO"),
		prov("agy", "agy", "GEMINI + CLAUDE/GPT"),
	}}
	snap := &model.Snapshot{
		AgyStatus:    "online",
		GeminiWeekly: bkt(47, false), Gemini5h: bkt(88, false),
		ClaudeWeekly: bkt(50, false), Claude5h: bkt(20, false),
		Providers: []*model.ProviderState{{
			Id: "opencode", Status: "online",
			OpenCode: &model.OpenCodeInfo{
				Monthly: model.Window{Name: "30д", Bucket: bkt(12, false)},
				Weekly:  model.Window{Name: "7д", Bucket: bkt(40, false)},
				Rolling: model.Window{Name: "5ч", Bucket: bkt(3, false)},
			},
		}},
	}

	sum := BuildSummary(snap, cfg)
	if len(sum.Items) != 3 {
		t.Fatalf("items = %d (%+v), want 3 (opencode + gemini + claude)", len(sum.Items), sum.Items)
	}
	if got := sum.Items[0].MenuTitle(); got != "OPENCODE GO — 30д 12% · 7д 40% · 5ч 3%" {
		t.Errorf("opencode title = %q", got)
	}
	if got := sum.Items[1].Short; got != "GEM" {
		t.Errorf("agy gemini short = %q", got)
	}
	// Худший процент — по всем показанным окнам, включая блоки AGY.
	if sum.WorstPercent != 88 {
		t.Errorf("worst = %v, want 88", sum.WorstPercent)
	}
}

// Провайдер без ключа не даёт процентов: ни в строке, ни в значке.
func TestBuildSummary_NoKey(t *testing.T) {
	cfg := &config.Config{Providers: []config.Provider{prov("kimi", "kimi", "KIMI")}}
	snap := &model.Snapshot{Providers: []*model.ProviderState{{
		Id: "kimi", Status: "no_key",
		Kimi: &model.KimiInfo{Limit7d: bkt(0, true), Limit5h: bkt(0, true)},
	}}}

	sum := BuildSummary(snap, cfg)
	if sum.State != StateUnknown {
		t.Errorf("state = %v, want StateUnknown", sum.State)
	}
	if got := sum.Items[0].MenuTitle(); got != "KIMI — нет ключа" {
		t.Errorf("menu title = %q", got)
	}
	if got := IconTip(sum.Items[0]); !strings.Contains(got, "нет ключа") {
		t.Errorf("tip = %q", got)
	}
}

// Заглушки (нет измеренных данных) рисуются как «--», а не как 0%.
func TestBuildSummary_PlaceholderRow(t *testing.T) {
	cfg := &config.Config{Providers: []config.Provider{prov("agy", "agy", "GEMINI + CLAUDE/GPT")}}
	snap := &model.Snapshot{
		AgyStatus:    "online",
		GeminiWeekly: bkt(0, true), Gemini5h: bkt(0, true),
		ClaudeWeekly: bkt(0, true), Claude5h: bkt(0, true),
	}
	sum := BuildSummary(snap, cfg)
	if len(sum.Items) != 2 {
		t.Fatalf("items = %+v, want GEMINI + CLAUDE/GPT", sum.Items)
	}
	if got := sum.Items[0].MenuTitle(); got != "GEMINI — 7д -- · 5ч --" {
		t.Errorf("menu title = %q", got)
	}
	if sum.WorstPercent != 0 {
		t.Errorf("worst = %v, placeholder must not count", sum.WorstPercent)
	}
}

// Один упавший провайдер не мешает остальным: его строка помечается, чужие
// проценты остаются.
func TestBuildSummary_StaleProvider(t *testing.T) {
	cfg := &config.Config{Providers: []config.Provider{
		prov("kimi", "kimi", "KIMI"),
		prov("openrouter", "openrouter", "OPENROUTER"),
	}}
	snap := &model.Snapshot{Providers: []*model.ProviderState{
		{Id: "kimi", Status: "stale", Kimi: &model.KimiInfo{Limit7d: bkt(64, false), Limit5h: bkt(11.3, false)}},
		{Id: "openrouter", Status: "online", OpenRouter: &model.OpenRouterInfo{
			UsedPercent: 4, UsedFraction: 0.04, FreeDailyUsed: 3, FreeDailyLimit: 1000,
		}},
	}}

	sum := BuildSummary(snap, cfg)
	if len(sum.Items) != 2 {
		t.Fatalf("items = %+v", sum.Items)
	}
	if got := sum.Items[0].MenuTitle(); got != "KIMI — 7д 64% · 5ч 11% (stale)" {
		t.Errorf("stale title = %q", got)
	}
	if got := sum.Items[1].MenuTitle(); got != "OPENROUTER — лимит 4% · free 3/1000" {
		t.Errorf("openrouter title = %q", got)
	}
}

// Скрытые настройкой окна не попадают ни в меню, ни в худший процент.
func TestBuildSummary_RespectsLimitsConfig(t *testing.T) {
	cfg := &config.Config{
		Providers: []config.Provider{prov("kimi", "kimi", "KIMI")},
		Limits:    map[string]map[string]bool{"kimi": {"7d": false}},
	}
	snap := &model.Snapshot{Providers: []*model.ProviderState{{
		Id: "kimi", Status: "online",
		Kimi: &model.KimiInfo{Limit7d: bkt(100, false), Limit5h: bkt(11.3, false)},
	}}}

	sum := BuildSummary(snap, cfg)
	if got := sum.Items[0].MenuTitle(); got != "KIMI — 5ч 11%" {
		t.Errorf("menu title = %q, want hidden 7d", got)
	}
	if sum.WorstPercent != 11.3 {
		t.Errorf("worst = %v, want 11.3", sum.WorstPercent)
	}
}

// До первого фетча сводки нет и значок остаётся без дуги.
func TestBuildSummary_NoSnapshot(t *testing.T) {
	cfg := &config.Config{Providers: []config.Provider{prov("kimi", "kimi", "KIMI")}}
	sum := BuildSummary(nil, cfg)
	if len(sum.Items) != 0 || sum.State != StateUnknown {
		t.Fatalf("sum = %+v, want empty unknown", sum)
	}
	if got := IconTip(Item{Label: "KIMI"}); !strings.Contains(got, "нет данных") {
		t.Errorf("tip = %q", got)
	}
}

// Подсказка значка укладывается в лимит оболочки и обрезается явно.
func TestFitTip_Truncation(t *testing.T) {
	long := strings.Repeat("OPENCODE 30д 12% · 7д 40% · 5ч 3% · ", 6)
	tip := fitTip(long)
	if got := utf16Len(tip); got > tooltipLimit {
		t.Fatalf("tip is %d UTF-16 units, limit is %d", got, tooltipLimit)
	}
	if !strings.HasSuffix(tip, "…") {
		t.Errorf("truncated tip must end with ellipsis, got %q", tip)
	}
	if short := fitTip("KIMI 7д 100%"); short != "KIMI 7д 100%" {
		t.Errorf("short tip = %q, must not be truncated", short)
	}
}

// У каждого значка свой худший процент и своё состояние: значок провайдера
// не должен показывать чужой расход.
func TestBuildSummary_PerItemWorst(t *testing.T) {
	cfg := &config.Config{Providers: []config.Provider{
		prov("kimi", "kimi", "KIMI"),
		prov("opencode", "opencode", "OPENCODE GO"),
	}}
	snap := &model.Snapshot{Providers: []*model.ProviderState{
		{Id: "kimi", Status: "online", Kimi: &model.KimiInfo{Limit7d: bkt(37.4, false), Limit5h: bkt(12, false)}},
		{Id: "opencode", Status: "online", OpenCode: &model.OpenCodeInfo{
			Monthly: model.Window{Name: "30д", Bucket: bkt(91, false)},
			Weekly:  model.Window{Name: "7д", Bucket: bkt(20, false)},
			Rolling: model.Window{Name: "5ч", Bucket: bkt(3, false)},
		}},
	}}

	sum := BuildSummary(snap, cfg)
	if len(sum.Items) != 2 {
		t.Fatalf("items = %+v", sum.Items)
	}
	if sum.Items[0].Worst != 37.4 || sum.Items[0].State != StateOK {
		t.Errorf("kimi worst = %v state = %v", sum.Items[0].Worst, sum.Items[0].State)
	}
	if sum.Items[1].Worst != 91 {
		t.Errorf("opencode worst = %v, want 91", sum.Items[1].Worst)
	}
	if sum.WorstPercent != 91 {
		t.Errorf("summary worst = %v, want 91", sum.WorstPercent)
	}
}

// Остаток до сброса доезжает до сводки: из него собирается подсказка полосы.
func TestBuildSummary_KeepsResetIn(t *testing.T) {
	cfg := &config.Config{Providers: []config.Provider{prov("kimi", "kimi", "KIMI")}}
	snap := &model.Snapshot{Providers: []*model.ProviderState{{
		Id: "kimi", Status: "online",
		Kimi: &model.KimiInfo{Limit7d: bkt(9, false), Limit5h: bkt(6, false)},
	}}}

	sum := BuildSummary(snap, cfg)
	vals := sum.Items[0].Values
	if len(vals) != 2 {
		t.Fatalf("values = %+v", vals)
	}
	if vals[0].Reset != "1d 2h" {
		t.Errorf("7d reset = %q, want the raw reset_in", vals[0].Reset)
	}
	if vals[0].String() != "7д 9%" {
		t.Errorf("value text = %q", vals[0].String())
	}
	// Меню и подсказка значков остаются без остатка: там важен каждый символ.
	if got := sum.Items[0].MenuTitle(); got != "KIMI — 7д 9% · 5ч 6%" {
		t.Errorf("menu title = %q", got)
	}
}

// Заглушка не притворяется значением: ни процента, ни остатка.
func TestBuildSummary_PlaceholderHasNoReset(t *testing.T) {
	cfg := &config.Config{Providers: []config.Provider{prov("kimi", "kimi", "KIMI")}}
	snap := &model.Snapshot{Providers: []*model.ProviderState{{
		Id: "kimi", Status: "online",
		Kimi: &model.KimiInfo{Limit7d: bkt(0, true), Limit5h: bkt(0, true)},
	}}}
	sum := BuildSummary(snap, cfg)
	for _, v := range sum.Items[0].Values {
		if v.Pct != "--" || v.Reset != "" {
			t.Errorf("placeholder value = %+v, want -- without reset", v)
		}
	}
}

// Подсказка отдельного значка — та же строка, что в меню, и в пределах лимита.
func TestIconTip(t *testing.T) {
	it := Item{Label: "OPENCODE GO", Short: "OPENCODE", Values: []Value{
		{Label: "30д", Pct: "12%"}, {Label: "7д", Pct: "40%"}, {Label: "5ч", Pct: "3%"},
	}}
	tip := IconTip(it)
	if tip != "OPENCODE GO 30д 12% · 7д 40% · 5ч 3%" {
		t.Errorf("tip = %q", tip)
	}
	if utf16Len(tip) > tooltipLimit {
		t.Errorf("tip is %d units, limit %d", utf16Len(tip), tooltipLimit)
	}
}
