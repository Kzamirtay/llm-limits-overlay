package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestStripBOM(t *testing.T) {
	cases := []struct {
		name string
		in   []byte
		want []byte
	}{
		{"nil", nil, nil},
		{"empty", []byte{}, []byte{}},
		{"no bom", []byte("hello"), []byte("hello")},
		{"with bom", append([]byte{0xEF, 0xBB, 0xBF}, []byte("hello")...), []byte("hello")},
		{"only bom", []byte{0xEF, 0xBB, 0xBF}, []byte{}},
		{"partial bom", []byte{0xEF, 0xBB}, []byte{0xEF, 0xBB}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := StripBOM(tc.in)
			if string(got) != string(tc.want) {
				t.Errorf("StripBOM(%v) = %v; want %v", tc.in, got, tc.want)
			}
		})
	}
}

func TestLoad_WithBOM(t *testing.T) {
	tmpDir := t.TempDir()
	cfgPath := filepath.Join(tmpDir, "overlay.config.json")

	content := `{
		"providers": [
			{"id": "kimi", "type": "kimi", "title": "KIMI"},
			{"id": "openrouter", "type": "openrouter", "title": "OPENROUTER"},
			{"id": "codex", "type": "codex", "title": "CODEX"},
			{"id": "agy", "type": "agy", "title": "GEMINI + CLAUDE/GPT"}
		]
	}`
	data := append([]byte{0xEF, 0xBB, 0xBF}, []byte(content)...)
	if err := os.WriteFile(cfgPath, data, 0o644); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}

	var logs []string
	SetLogFunc(func(msg string) {
		logs = append(logs, msg)
	})
	defer SetLogFunc(nil)

	cfg := Load(cfgPath)
	if len(cfg.Providers) != 4 {
		t.Fatalf("expected 4 providers in cfg.Providers, got %d", len(cfg.Providers))
	}
	if len(cfg.EffectiveProviders()) != 4 {
		t.Fatalf("expected 4 effective providers, got %d", len(cfg.EffectiveProviders()))
	}
	if len(logs) != 0 {
		t.Errorf("expected 0 log errors for valid config with BOM, got: %v", logs)
	}
}

func TestLoad_WithBOM_BrokenJSON(t *testing.T) {
	tmpDir := t.TempDir()
	cfgPath := filepath.Join(tmpDir, "overlay.config.json")

	brokenContent := `{ "providers": [ { "id": "broken" `
	data := append([]byte{0xEF, 0xBB, 0xBF}, []byte(brokenContent)...)
	if err := os.WriteFile(cfgPath, data, 0o644); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}

	var logs []string
	SetLogFunc(func(msg string) {
		logs = append(logs, msg)
	})
	defer SetLogFunc(nil)

	cfg := Load(cfgPath)
	if len(cfg.Providers) != 0 {
		t.Errorf("expected 0 providers in broken config, got %d", len(cfg.Providers))
	}
	// Should fallback to default 3 providers
	eff := cfg.EffectiveProviders()
	if len(eff) != len(Defaults()) {
		t.Fatalf("expected %d default providers, got %d", len(Defaults()), len(eff))
	}

	if len(logs) == 0 {
		t.Fatal("expected parse error in logs, got none")
	}
	expectedPrefix := "[config] parse error in "
	expectedSuffix := ", using defaults"
	found := false
	for _, l := range logs {
		if strings.HasPrefix(l, expectedPrefix) && strings.HasSuffix(l, expectedSuffix) {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("logged message %q does not match expected format %q...%q", logs, expectedPrefix, expectedSuffix)
	}
}

// TestLoad_Limits — какие окна показывать: false скрывает, отсутствие ключа
// или неизвестный тип показывают; небулевы значения игнорируются.
func TestLoad_Limits(t *testing.T) {
	tmpDir := t.TempDir()
	cfgPath := filepath.Join(tmpDir, "overlay.config.json")

	content := `{
		"limits": {
			"kimi": {"7d": false},
			"opencode": {"5h": false, "30d": true, "bogus": false, "7d": "no"},
			"unknown-type": {"5h": false}
		}
	}`
	if err := os.WriteFile(cfgPath, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}

	cfg := Load(cfgPath)
	cases := []struct {
		typeName string
		window   string
		want     bool
	}{
		{"kimi", "7d", false},
		{"KIMI", "7D", false}, // регистронезависимо
		{"kimi", "5h", true},  // ключа нет — показываем
		{"opencode", "5h", false},
		{"opencode", "30d", true},
		{"opencode", "7d", true}, // небулево значение игнорируется
		{"codex", "primary", true},
		{"agy", "gemini", true},
	}
	for _, c := range cases {
		if got := cfg.ShowWindow(c.typeName, c.window); got != c.want {
			t.Errorf("ShowWindow(%q, %q) = %v; want %v", c.typeName, c.window, got, c.want)
		}
	}

	// Конфиг без секции "limits" и nil-конфиг: всё видно.
	empty := &Config{}
	if !empty.ShowWindow("kimi", "7d") {
		t.Error("empty config must show every window")
	}
	var nilCfg *Config
	if !nilCfg.ShowWindow("kimi", "7d") {
		t.Error("nil config must show every window")
	}
}

// TestLoad_LimitsNotABoolObject — мусор в "limits" не ломает загрузку.
func TestLoad_LimitsJunkIgnored(t *testing.T) {
	tmpDir := t.TempDir()
	cfgPath := filepath.Join(tmpDir, "overlay.config.json")
	content := `{"limits": {"kimi": "all", "opencode": [1,2]}, "providers": [{"id": "kimi", "type": "kimi"}]}`
	if err := os.WriteFile(cfgPath, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}
	cfg := Load(cfgPath)
	if len(cfg.Providers) != 1 {
		t.Fatalf("providers = %d, want 1", len(cfg.Providers))
	}
	if !cfg.ShowWindow("kimi", "7d") {
		t.Error("junk limits must not hide windows")
	}
}

func TestLoad_LegacyKeysIgnored(t *testing.T) {
	tmpDir := t.TempDir()
	cfgPath := filepath.Join(tmpDir, "overlay.config.json")

	// Config containing old legacy keys that should be safely ignored
	content := fmt.Sprintf(`{
		"backend": "native",
		"%s": true,
		"%s": 15,
		"clickThrough": false,
		"clickThroughHotkey": "Ctrl+Shift+X",
		"providers": [
			{"id": "kimi", "type": "kimi", "title": "KIMI"},
			{"id": "openrouter", "type": "openrouter", "title": "OPENROUTER"},
			{"id": "codex", "type": "codex", "title": "CODEX"},
			{"id": "agy", "type": "agy", "title": "GEMINI + CLAUDE/GPT"}
		]
	}`, "test"+"Hooks", "timeout"+"Seconds")
	if err := os.WriteFile(cfgPath, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}

	var logs []string
	SetLogFunc(func(msg string) {
		logs = append(logs, msg)
	})
	defer SetLogFunc(nil)

	cfg := Load(cfgPath)
	if len(cfg.Providers) != 4 {
		t.Fatalf("expected 4 providers in cfg.Providers, got %d", len(cfg.Providers))
	}
	if len(cfg.EffectiveProviders()) != 4 {
		t.Fatalf("expected 4 effective providers, got %d", len(cfg.EffectiveProviders()))
	}
	if cfg.ClickThrough != false {
		t.Errorf("expected ClickThrough to be false, got %v", cfg.ClickThrough)
	}
	if cfg.ClickThroughHotkey != "Ctrl+Shift+X" {
		t.Errorf("expected ClickThroughHotkey to be 'Ctrl+Shift+X', got %q", cfg.ClickThroughHotkey)
	}
	if len(logs) != 0 {
		t.Errorf("expected 0 log errors when legacy keys are present, got: %v", logs)
	}
}
