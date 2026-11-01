package env

import (
	"os"
	"path/filepath"
	"testing"
)

func TestNewParser_WithBOM(t *testing.T) {
	tmpDir := t.TempDir()
	envPath := filepath.Join(tmpDir, ".env")

	content := "OPENROUTER_API_KEY=sk-or-test123456\nKIMI_API_KEY=\"sk-kimi-test789\"\n"
	data := append([]byte{0xEF, 0xBB, 0xBF}, []byte(content)...)
	if err := os.WriteFile(envPath, data, 0o644); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}

	p := NewParser(envPath)
	if got := p.Get("OPENROUTER_API_KEY"); got != "sk-or-test123456" {
		t.Errorf("OPENROUTER_API_KEY = %q; want %q", got, "sk-or-test123456")
	}
	if got := p.Get("KIMI_API_KEY"); got != "sk-kimi-test789" {
		t.Errorf("KIMI_API_KEY = %q; want %q", got, "sk-kimi-test789")
	}
}

func TestNewParser_WithoutBOM(t *testing.T) {
	tmpDir := t.TempDir()
	envPath := filepath.Join(tmpDir, ".env")

	content := "OPENROUTER_API_KEY=sk-plain\n"
	if err := os.WriteFile(envPath, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}

	p := NewParser(envPath)
	if got := p.Get("OPENROUTER_API_KEY"); got != "sk-plain" {
		t.Errorf("OPENROUTER_API_KEY = %q; want %q", got, "sk-plain")
	}
}
