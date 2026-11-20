package trayui

import (
	"testing"
)

// Все вложенные иконки должны читаться и иметь осмысленное покрытие:
// пустая маска означала бы «нарисовали ничего» и молчаливую дырку в полосе.
func TestLoadIcon_AllAssets(t *testing.T) {
	for _, name := range []string{"moonshotai", "opencode", "googlegemini", "anthropic", "openrouter"} {
		m, err := LoadIcon(name)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if m.Size < 16 {
			t.Errorf("%s: size %d", name, m.Size)
		}
		if cov := m.Coverage(); cov < 0.10 || cov > 0.85 {
			t.Errorf("%s: coverage %.2f looks wrong", name, cov)
		}
	}
}

func TestLoadIcon_Unknown(t *testing.T) {
	if _, err := LoadIcon("nope"); err == nil {
		t.Fatal("unknown icon must return an error")
	}
	if _, err := LoadIcon(""); err == nil {
		t.Fatal("empty name must return an error")
	}
}

// Масштабирование сохраняет форму: покрытие почти не меняется, размер точный.
func TestIconMask_Scale(t *testing.T) {
	m, err := LoadIcon("googlegemini")
	if err != nil {
		t.Fatal(err)
	}
	for _, size := range []int{12, 16, 20, 24, 32} {
		s := m.Scale(size)
		if s.Size != size || len(s.Alpha) != size*size {
			t.Fatalf("scale %d: got size %d len %d", size, s.Size, len(s.Alpha))
		}
		if cov := s.Coverage(); cov < 0.05 {
			t.Errorf("scale %d: coverage %.2f is too low", size, cov)
		}
	}
	if m.Scale(m.Size) != m {
		t.Error("scaling to the same size must reuse the mask")
	}
	if (&IconMask{}).Scale(0).Size != 0 {
		t.Error("zero size must produce an empty mask")
	}
}

// Иконки выбираются по типу провайдера; у AGY — по блоку карточки.
func TestIconFor(t *testing.T) {
	cases := []struct{ provType, label, want string }{
		{"kimi", "KIMI", "moonshotai"},
		{"openrouter", "OPENROUTER", "openrouter"},
		{"opencode", "OPENCODE GO", "opencode"},
		{"codex", "CODEX", ""},
		{"agy", "GEMINI", "googlegemini"},
		{"agy", "CLAUDE/GPT", "anthropic"},
		{"custom", "MYPROVIDER", ""},
	}
	for _, c := range cases {
		if got := IconFor(c.provType, c.label); got != c.want {
			t.Errorf("IconFor(%q,%q) = %q, want %q", c.provType, c.label, got, c.want)
		}
	}
}
