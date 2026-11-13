package fetchers

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"llmlimits/internal/config"
)

// Живая схема https://opencode.ai/zen/go/v1/usage (OpenCode CLI 2.2.5,
// server.ts: EA(...) -> {usage:{rolling,weekly,monthly}}).
const openCodeFixture = `{
  "usage": {
    "rolling": {"status": "ok", "percent": 8.4, "resetsAt": "2099-11-13T15:43:53.000Z"},
    "weekly":  {"status": "rate-limited", "percent": 100, "resetsAt": "2099-11-16T00:00:00.000Z"},
    "monthly": {"status": "ok", "percent": 41.25, "resetsAt": "2099-12-13T10:33:31.000Z"}
  }
}`

func openCodeProvider(baseURL, key string) config.Provider {
	return config.Provider{Id: "opencode", Type: "opencode", Title: "OPENCODE GO", BaseUrl: baseURL, ApiKey: key, Enabled: true}
}

func TestOpenCode_ParsesWindows(t *testing.T) {
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		if r.URL.Path != "/usage" {
			t.Errorf("path = %s, want /usage", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer sk-test" {
			t.Errorf("Authorization = %q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(openCodeFixture))
	}))
	defer srv.Close()

	appDir := t.TempDir()
	block := FetchOpenCode(appDir, openCodeProvider(srv.URL, "sk-test"), nil)

	if hits != 1 {
		t.Fatalf("HTTP hits = %d, want 1", hits)
	}
	if got := getStr(block, "status"); got != "online" {
		t.Fatalf("status = %q, want online", got)
	}
	if got := getStr(block, "plan"); got != "go" {
		t.Errorf("plan = %q, want go", got)
	}

	rolling := getMap(block, "rolling")
	if rolling == nil {
		t.Fatal("rolling window missing")
	}
	if got := getFloat(rolling, "used_percent", -1); got != 8.4 {
		t.Errorf("rolling.used_percent = %v, want 8.4", got)
	}
	if got := getFloat(rolling, "used_ratio", -1); got != 0.084 {
		t.Errorf("rolling.used_ratio = %v, want 0.084", got)
	}
	if got := getStr(rolling, "level"); got != "ok" {
		t.Errorf("rolling.level = %q, want ok", got)
	}
	if got := getFloat(rolling, "remaining_usd", -1); got != 10.99 {
		t.Errorf("rolling.remaining_usd = %v, want 10.99", got)
	}
	if got := getStr(rolling, "reset_in"); got == "--" || got == "" {
		t.Errorf("rolling.reset_in = %q, want human diff", got)
	}
	if getBool(rolling, "placeholder") {
		t.Error("rolling marked placeholder despite measured percent")
	}

	weekly := getMap(block, "weekly")
	if got := getStr(weekly, "level"); got != "critical" {
		t.Errorf("weekly.level = %q, want critical", got)
	}
	if !getBool(weekly, "limited") {
		t.Error("weekly.limited = false, want true for status rate-limited")
	}
	if got := getFloat(weekly, "remaining_usd", -1); got != 0 {
		t.Errorf("weekly.remaining_usd = %v, want 0", got)
	}

	monthly := getMap(block, "monthly")
	if got := getStr(monthly, "level"); got != "warning" {
		t.Errorf("monthly.level = %q, want warning (41.25%%)", got)
	}
}

// Отсутствующее окно не должно превращаться в выдуманный «0% использовано».
func TestOpenCode_MissingWindowIsPlaceholder(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"usage":{"rolling":{"status":"ok","percent":5,"resetsAt":"2099-11-13T15:43:53.000Z"}}}`))
	}))
	defer srv.Close()

	block := FetchOpenCode(t.TempDir(), openCodeProvider(srv.URL, "sk-test"), nil)
	monthly := getMap(block, "monthly")
	if monthly == nil {
		t.Fatal("monthly window missing")
	}
	if !getBool(monthly, "placeholder") {
		t.Error("monthly.placeholder = false, want true")
	}
	if got := getStr(monthly, "level"); got != "unknown" {
		t.Errorf("monthly.level = %q, want unknown", got)
	}

	st := OpenCodeToState(block)
	if !st.Monthly.Placeholder {
		t.Error("state: monthly not placeholder")
	}
	if st.Rolling.Placeholder {
		t.Error("state: rolling wrongly placeholder")
	}
	if st.Rolling.UsedPercent != 5 {
		t.Errorf("state rolling percent = %v, want 5", st.Rolling.UsedPercent)
	}
	if st.Monthly.LimitUSD != 0 {
		t.Errorf("state monthly limit_usd = %v, want 0 for placeholder", st.Monthly.LimitUSD)
	}
}

// 401 (ключ без подписки Go / отозванный ключ) — честная ошибка, не нули.
func TestOpenCode_UnauthorizedIsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"type":"error","error":{"type":"AuthError","message":"Unauthorized"}}`))
	}))
	defer srv.Close()

	block := FetchOpenCode(t.TempDir(), openCodeProvider(srv.URL, "sk-test"), nil)
	if got := getStr(block, "status"); got != "error" {
		t.Fatalf("status = %q, want error", got)
	}
	if !strings.Contains(getStr(block, "error"), "401") {
		t.Errorf("error = %q, want HTTP 401", getStr(block, "error"))
	}
	if !getBool(getMap(block, "rolling"), "placeholder") {
		t.Error("rolling not placeholder on auth error")
	}
}

func TestOpenCode_PlaceholderKeySkipsHTTP(t *testing.T) {
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		_, _ = w.Write([]byte(openCodeFixture))
	}))
	defer srv.Close()

	appDir := t.TempDir()
	block := FetchOpenCode(appDir, openCodeProvider(srv.URL, "sk-...here"), nil)
	if hits != 0 {
		t.Errorf("HTTP hits = %d, want 0 for placeholder key", hits)
	}
	if got := getStr(block, "status"); got != "no_key" {
		t.Errorf("status = %q, want no_key", got)
	}
}

// Сбой сети после успешного цикла: показываем прошлые цифры со stale,
// а не обнуляем карточку.
func TestOpenCode_StaleKeepsPreviousReadings(t *testing.T) {
	appDir := t.TempDir()
	ok := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(openCodeFixture))
	}))
	FetchOpenCode(appDir, openCodeProvider(ok.URL, "sk-test"), nil)
	ok.Close()

	broken := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer broken.Close()

	block := FetchOpenCode(appDir, openCodeProvider(broken.URL, "sk-test"), nil)
	if got := getStr(block, "status"); got != "stale" {
		t.Fatalf("status = %q, want stale", got)
	}
	if got := getFloat(getMap(block, "monthly"), "used_percent", -1); got != 41.3 {
		t.Errorf("monthly.used_percent = %v, want cached 41.3", got)
	}
}
