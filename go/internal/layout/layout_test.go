package layout

import (
	"testing"

	"llmlimits/internal/config"
	"llmlimits/internal/model"
)

func bucket(pct float64, placeholder bool) model.Bucket {
	return model.Bucket{UsedPercent: pct, UsedFraction: pct / 100.0, ResetIn: "1d 2h", Placeholder: placeholder}
}

func provider(typ string) config.Provider {
	return config.Provider{Id: typ, Type: typ, Enabled: true}
}

func kimiState(weekly, five float64) *model.ProviderState {
	return &model.ProviderState{Id: "kimi", Kind: "kimi", Status: "online",
		Kimi: &model.KimiInfo{Limit7d: bucket(weekly, false), Limit5h: bucket(five, false)}}
}

// Исчерпанный недельный лимит отменяет показ часового.
func TestKimi_HidesShorterWhenWeeklyExhausted(t *testing.T) {
	rows := KimiRowList(&config.Config{}, provider("kimi"), kimiState(100, 11.3))
	if len(rows) != 1 || rows[0].Key != "7d" {
		t.Fatalf("rows = %+v, want single 7d row", rows)
	}
}

func TestKimi_KeepsBothWhileNotExhausted(t *testing.T) {
	rows := KimiRowList(&config.Config{}, provider("kimi"), kimiState(99.9, 11.3))
	if len(rows) != 2 || rows[0].Key != "7d" || rows[1].Key != "5h" {
		t.Fatalf("rows = %+v, want 7d + 5h", rows)
	}
}

// Скрытый настройкой длинный лимит не влияет на короткий.
func TestKimi_ConfigHidesLongWindow(t *testing.T) {
	cfg := &config.Config{Limits: map[string]map[string]bool{"kimi": {"7d": false}}}
	rows := KimiRowList(cfg, provider("kimi"), kimiState(100, 11.3))
	if len(rows) != 1 || rows[0].Key != "5h" {
		t.Fatalf("rows = %+v, want single 5h row", rows)
	}
}

func openCodeState(monthly float64, weekly float64, weeklyLimited bool, five float64) *model.ProviderState {
	return &model.ProviderState{Id: "opencode", Kind: "opencode", Status: "online",
		OpenCode: &model.OpenCodeInfo{
			Rolling: model.Window{Name: "5ч", Bucket: bucket(five, false)},
			Weekly:  model.Window{Name: "7д", Bucket: bucket(weekly, false), Limited: weeklyLimited},
			Monthly: model.Window{Name: "30д", Bucket: bucket(monthly, false)},
		}}
}

// Исчерпанный месячный лимит схлопывает окна до одного.
func TestOpenCode_MonthlyExhausted(t *testing.T) {
	windows := OpenCodeWindows(&config.Config{}, provider("opencode"), openCodeState(100, 40, false, 5))
	if len(windows) != 1 || windows[0].Name != "30д" {
		t.Fatalf("windows = %+v, want single 30д", windows)
	}
}

// status=rate-limited считается исчерпанием даже при проценте < 100.
func TestOpenCode_RateLimitedCountsAsExhausted(t *testing.T) {
	windows := OpenCodeWindows(&config.Config{}, provider("opencode"), openCodeState(40, 97, true, 5))
	if len(windows) != 2 || windows[0].Name != "30д" || windows[1].Name != "7д" {
		t.Fatalf("windows = %+v, want 30д + 7д", windows)
	}
}

// Заглушка (нет данных) не считается исчерпанной.
func TestOpenCode_PlaceholderDoesNotCollapse(t *testing.T) {
	st := &model.ProviderState{OpenCode: &model.OpenCodeInfo{
		Rolling: model.Window{Name: "5ч", Bucket: bucket(0, false)},
		Weekly:  model.Window{Name: "7д", Bucket: bucket(0, false)},
		Monthly: model.Window{Name: "30д", Bucket: bucket(0, true)},
	}}
	if windows := OpenCodeWindows(&config.Config{}, provider("opencode"), st); len(windows) != 3 {
		t.Fatalf("windows = %d, want 3 (placeholder is not exhausted)", len(windows))
	}
}

// Codex: длину окна определяют window_mins, а не порядок в карточке.
func TestCodex_LongestExhaustedKeepsOnlyIt(t *testing.T) {
	st := &model.ProviderState{Codex: &model.CodexInfo{
		Primary:      bucket(20, false),
		PrimaryMin:   300,   // 5ч
		Secondary:    ptrBucket(100), // неделя
		SecondaryMin: 10080,
	}}
	rows := CodexRowList(&config.Config{}, provider("codex"), st)
	if len(rows) != 1 || rows[0].Key != "secondary" {
		t.Fatalf("rows = %+v, want only secondary", rows)
	}
}

func TestCodex_ShortExhaustedKeepsBoth(t *testing.T) {
	st := &model.ProviderState{Codex: &model.CodexInfo{
		Primary:      bucket(100, false),
		PrimaryMin:   300,
		Secondary:    ptrBucket(40),
		SecondaryMin: 10080,
	}}
	if rows := CodexRowList(&config.Config{}, provider("codex"), st); len(rows) != 2 {
		t.Fatalf("rows = %d, want 2 (short window exhaustion is not a cascade)", len(rows))
	}
}

func TestAgy_WeeklyExhaustedHides5h(t *testing.T) {
	rows := AgyRowList(true, bucket(100, false), bucket(0, false))
	if len(rows) != 1 || rows[0].Key != "7d" {
		t.Fatalf("rows = %+v, want single 7d", rows)
	}
	if rows := AgyRowList(false, bucket(100, false), bucket(0, false)); len(rows) != 0 {
		t.Fatalf("disabled group must yield no rows, got %d", len(rows))
	}
}

func TestHeights(t *testing.T) {
	cases := []struct {
		name string
		got  float32
		want float32
	}{
		{"kimi 2 rows", KimiHeight(2), 94},
		{"kimi 1 row", KimiHeight(1), 64},
		{"kimi 0 rows", KimiHeight(0), 0},
		{"opencode 3 rows", OpenCodeHeight(3), 124},
		{"opencode 1 row", OpenCodeHeight(1), 68},
		{"codex 2 rows", CodexHeight(2), 94},
		{"codex 1 row", CodexHeight(1), 62},
		{"openrouter both", OpenRouterHeight(true, true), 91},
		{"openrouter one", OpenRouterHeight(true, false), 62},
		{"openrouter none", OpenRouterHeight(false, false), 0},
		{"agy both groups", AgyHeight(2, 2), 186},
		{"agy gemini only", AgyHeight(2, 0), 89},
		{"agy claude only", AgyHeight(0, 2), 89},
		{"agy one row each", AgyHeight(1, 1), 126},
	}
	for _, c := range cases {
		if c.got != c.want {
			t.Errorf("%s = %v, want %v", c.name, c.got, c.want)
		}
	}
}

// CardHeight связывает настройку, исчерпание и высоту карточки.
func TestCardHeight_OpencodeWithHiddenAndExhausted(t *testing.T) {
	cfg := &config.Config{}
	p := provider("opencode")
	if got := CardHeight(cfg, p, openCodeState(0, 0, false, 0), nil); got != 124 {
		t.Errorf("all windows = %v, want 124", got)
	}
	cfg.Limits = map[string]map[string]bool{"opencode": {"5h": false}}
	if got := CardHeight(cfg, p, openCodeState(0, 0, false, 0), nil); got != 96 {
		t.Errorf("hidden 5h = %v, want 96", got)
	}
	if got := CardHeight(&config.Config{}, p, openCodeState(0, 100, false, 0), nil); got != 96 {
		t.Errorf("exhausted weekly = %v, want 96", got)
	}
	if got := CardHeight(&config.Config{}, p, openCodeState(100, 0, false, 0), nil); got != 68 {
		t.Errorf("exhausted monthly = %v, want 68", got)
	}
}

func ptrBucket(pct float64) *model.Bucket {
	b := bucket(pct, false)
	return &b
}
