package config

import (
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
