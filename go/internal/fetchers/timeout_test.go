package fetchers

import (
	"runtime"
	"testing"
)

func TestDefaultAgyTimeoutSec(t *testing.T) {
	want := 20
	if runtime.GOOS == "windows" {
		want = 25 // agy.exe ~230 МБ: холодный старт 6–8 с, 8 с из 2.2.0 не хватало
	}
	if got := DefaultAgyTimeoutSec(); got != want {
		t.Fatalf("DefaultAgyTimeoutSec() = %d; want %d", got, want)
	}
}

func TestResolveAgyTimeout_Precedence(t *testing.T) {
	// env задан и должен перебивать конфиг, но не явный флаг
	t.Setenv("LLM_LIMITS_AGY_TIMEOUT", "33")

	cases := []struct {
		name    string
		flagSec int
		cfgSec  int
		want    int
	}{
		{"flag wins over env and config", 7, 11, 7},
		{"env wins over config", 0, 11, 33},
		{"env wins over default", 0, 0, 33},
		{"garbage config does not break env", 0, 999, 33},
		{"flag out of range falls through to env", 121, 11, 33},
		{"negative flag falls through to env", -5, 0, 33},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ResolveAgyTimeout(tc.flagSec, tc.cfgSec); got != tc.want {
				t.Fatalf("ResolveAgyTimeout(%d, %d) = %d; want %d", tc.flagSec, tc.cfgSec, got, tc.want)
			}
		})
	}
}

func TestResolveAgyTimeout_Fallback(t *testing.T) {
	t.Setenv("LLM_LIMITS_AGY_TIMEOUT", "")
	def := DefaultAgyTimeoutSec()

	cases := []struct {
		name    string
		flagSec int
		cfgSec  int
		want    int
	}{
		{"config wins over default", 0, 40, 40},
		{"config bounds are allowed", 0, 120, 120},
		{"config out of range ignored", 0, 0, def},
		{"config too big ignored", 0, 500, def},
		{"negative config ignored", 0, -1, def},
		{"nothing set uses platform default", 0, 0, def},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ResolveAgyTimeout(tc.flagSec, tc.cfgSec); got != tc.want {
				t.Fatalf("ResolveAgyTimeout(%d, %d) = %d; want %d", tc.flagSec, tc.cfgSec, got, tc.want)
			}
		})
	}
}

func TestAgyTimeoutFromEnv_Garbage(t *testing.T) {
	for _, v := range []string{"", "abc", "0", "121", "-3", "12s"} {
		t.Setenv("LLM_LIMITS_AGY_TIMEOUT", v)
		if got := AgyTimeoutFromEnv(); got != 0 {
			t.Fatalf("AgyTimeoutFromEnv() with %q = %d; want 0 (ignored)", v, got)
		}
	}
	t.Setenv("LLM_LIMITS_AGY_TIMEOUT", " 45 ")
	if got := AgyTimeoutFromEnv(); got != 45 {
		t.Fatalf("AgyTimeoutFromEnv() with spaces = %d; want 45", got)
	}
}
