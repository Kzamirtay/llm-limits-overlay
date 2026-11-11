//go:build windows

package main

import "testing"

func TestFormatPercent(t *testing.T) {
	cases := []struct {
		in   float64
		want string
	}{
		{0.0, "0%"},
		{10.0, "10%"},
		{42.7, "42,7%"},
		{100.0, "100%"},
		{0.5, "0,5%"},
		{42.0, "42%"},
	}

	for _, c := range cases {
		got := formatPercent(c.in)
		if got != c.want {
			t.Errorf("formatPercent(%v) = %q; want %q", c.in, got, c.want)
		}
	}
}

// TestAgyBucketPctText — заглушка (placeholder) даёт "--" без бара,
// реальное значение (включая честный 0%%) даёт отформатированный процент с баром.
func TestAgyBucketPctText(t *testing.T) {
	text, ok := agyBucketPctText(true, 0.0)
	if text != "--" || ok {
		t.Errorf("placeholder: got (%q, %v); want (\"--\", false)", text, ok)
	}
	text, ok = agyBucketPctText(false, 0.0)
	if text != "0%" || !ok {
		t.Errorf("real 0%%: got (%q, %v); want (\"0%%\", true)", text, ok)
	}
	text, ok = agyBucketPctText(false, 42.7)
	if text != "42,7%" || !ok {
		t.Errorf("real 42.7%%: got (%q, %v); want (\"42,7%%\", true)", text, ok)
	}
}
