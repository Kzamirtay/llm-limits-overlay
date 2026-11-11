package fetchers

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// ---- helpers ----

// tempHome creates a temp directory tree simulating a user home.
func tempHome(t *testing.T) (string, func()) {
	t.Helper()
	dir, err := os.MkdirTemp("", "agy-auth-test-*")
	if err != nil {
		t.Fatalf("tempHome: %v", err)
	}
	return dir, func() { os.RemoveAll(dir) }
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
}

// checkAgyAuthInDir runs the REAL production validator (agyAuthStateForPaths)
// against an explicit home dir — no dependence on the OS HOME of the test runner.
func checkAgyAuthInDir(home string) bool {
	ok, _ := agyAuthStateForPaths(agyAuthPaths(home, ""))
	return ok
}

// useFakeAgyHome redirects the production auth lookup to a temp home for the
// duration of the test. Returns the temp home.
func useFakeAgyHome(t *testing.T) string {
	t.Helper()
	home, cleanup := tempHome(t)
	t.Cleanup(func() {
		agyHomeOverride = ""
		cleanup()
	})
	agyHomeOverride = home
	return home
}

// useFakeAgyCache redirects the agy usage cache to a temp file.
func useFakeAgyCache(t *testing.T) {
	t.Helper()
	cacheFile := filepath.Join(t.TempDir(), "agy_usage_cache.json")
	t.Cleanup(func() {
		agyCacheFileOverride = ""
		agyMemCache = nil
		agyBypassCache = false
	})
	agyCacheFileOverride = cacheFile
	agyMemCache = nil
}

// useFakeAgyBin points LLM_LIMITS_AGY_BIN at a fake child script.
// The script appends one line to counterPath per invocation.
// authFail=true → exit 1 with auth error text; otherwise print usageJSON and exit 0.
func useFakeAgyBin(t *testing.T, counterPath, usageJSON string, authFail bool) {
	t.Helper()
	dir := t.TempDir()
	var scriptPath, content string
	if runtime.GOOS == "windows" {
		scriptPath = filepath.Join(dir, "fake-agy.bat")
		if authFail {
			content = fmt.Sprintf("@echo off\r\necho auth required: please run agy auth login 1>&2\r\nexit /b 1\r\n")
		} else {
			content = fmt.Sprintf("@echo off\r\necho %s\r\nexit /b 0\r\n", usageJSON)
		}
	} else {
		scriptPath = filepath.Join(dir, "fake-agy.sh")
		if authFail {
			content = fmt.Sprintf("#!/bin/sh\necho x >> '%s'\necho 'auth required: please run agy auth login' 1>&2\nexit 1\n", counterPath)
		} else {
			content = fmt.Sprintf("#!/bin/sh\necho x >> '%s'\necho '%s'\nexit 0\n", counterPath, usageJSON)
		}
	}
	writeFile(t, scriptPath, content)
	if runtime.GOOS != "windows" {
		if err := os.Chmod(scriptPath, 0o755); err != nil {
			t.Fatalf("chmod: %v", err)
		}
	}
	t.Setenv("LLM_LIMITS_AGY_BIN", scriptPath)
}

func countExecs(t *testing.T, counterPath string) int {
	t.Helper()
	b, err := os.ReadFile(counterPath)
	if err != nil {
		return 0
	}
	return len(strings.Split(strings.TrimRight(string(b), "\n"), "\n"))
}

func resetLatch() {
	agyAuthLatch.failed = false
	agyAuthLatch.fingerprint = ""
	agyAuthLatch.logged = false
}

const validAcpJSON = `{"client_id":"x","client_secret":"s","refresh_token":"y","token_uri":"https://oauth2.googleapis.com/token","scopes":["aicode"],"project_id":"aicode-consumers"}`
const validOauthJSON = `{"access_token":"tok","refresh_token":"rtok","scope":"cloud-platform","token_type":"Bearer","id_token":"idtok","expiry_date":9999999999999}`

// ---- structural credential validation ----

// TestAgy_AcpTokenOnly: structurally valid acp_token.json (AGY 1.5.4 real cred file)
func TestAgy_AcpTokenOnly(t *testing.T) {
	home, cleanup := tempHome(t)
	defer cleanup()
	writeFile(t, filepath.Join(home, ".gemini", "antigravity-acp", "acp_token.json"), validAcpJSON)

	if !checkAgyAuthInDir(home) {
		t.Errorf("expected auth=true for valid acp_token.json, got false")
	}
}

// TestAgy_OauthCredsOnly: oauth_creds.json with non-empty refresh_token (legacy path)
func TestAgy_OauthCredsOnly(t *testing.T) {
	home, cleanup := tempHome(t)
	defer cleanup()
	writeFile(t, filepath.Join(home, ".gemini", "oauth_creds.json"), validOauthJSON)

	if !checkAgyAuthInDir(home) {
		t.Errorf("expected auth=true for oauth_creds.json with refresh_token, got false")
	}
}

// TestAgy_LegacyTokenOnly: opaque legacy token file (not JSON) stays supported
func TestAgy_LegacyTokenOnly(t *testing.T) {
	home, cleanup := tempHome(t)
	defer cleanup()
	writeFile(t, filepath.Join(home, ".gemini", "antigravity-cli", "antigravity-oauth-token"), "some-legacy-token-value")

	if !checkAgyAuthInDir(home) {
		t.Errorf("expected auth=true for legacy antigravity-oauth-token, got false")
	}
}

func TestAgy_NoCreds(t *testing.T) {
	home, cleanup := tempHome(t)
	defer cleanup()

	if checkAgyAuthInDir(home) {
		t.Errorf("expected auth=false when no cred files present, got true")
	}
}

func TestAgy_EmptyFile(t *testing.T) {
	home, cleanup := tempHome(t)
	defer cleanup()
	writeFile(t, filepath.Join(home, ".gemini", "antigravity-acp", "acp_token.json"), "")

	if checkAgyAuthInDir(home) {
		t.Errorf("expected auth=false when cred file is empty, got true")
	}
}

// TestAgy_MalformedJson_Blocked: malformed JSON must NOT count as real auth —
// the old fileNonEmpty check accepted garbage and risked an OAuth popup storm.
func TestAgy_MalformedJson_Blocked(t *testing.T) {
	home, cleanup := tempHome(t)
	defer cleanup()
	writeFile(t, filepath.Join(home, ".gemini", "antigravity-acp", "acp_token.json"), "not valid json")

	if checkAgyAuthInDir(home) {
		t.Errorf("expected auth=false for malformed acp_token.json, got true")
	}
}

// TestAgy_AcpMissingRequiredFields: valid JSON but no refresh_token → not AGY auth
func TestAgy_AcpMissingRequiredFields(t *testing.T) {
	home, cleanup := tempHome(t)
	defer cleanup()
	// structurally valid JSON, but nothing AGY can refresh a session from
	writeFile(t, filepath.Join(home, ".gemini", "antigravity-acp", "acp_token.json"),
		`{"client_id":"x","project_id":"aicode-consumers","note":"no refresh_token here"}`)

	if checkAgyAuthInDir(home) {
		t.Errorf("expected auth=false when acp_token.json lacks refresh_token, got true")
	}
}

// TestAgy_OauthWithoutRefreshToken: Gemini CLI creds without refresh_token are
// irrelevant for AGY auth (expired access token alone cannot silently re-auth).
func TestAgy_OauthWithoutRefreshToken(t *testing.T) {
	home, cleanup := tempHome(t)
	defer cleanup()
	writeFile(t, filepath.Join(home, ".gemini", "oauth_creds.json"),
		`{"access_token":"expired","expiry_date":1762000000000}`)

	if checkAgyAuthInDir(home) {
		t.Errorf("expected auth=false when oauth_creds.json has no refresh_token, got true")
	}
}

// TestAgy_CredPathIsDirectory: a directory at the cred path is not a credential
func TestAgy_CredPathIsDirectory(t *testing.T) {
	home, cleanup := tempHome(t)
	defer cleanup()
	dirPath := filepath.Join(home, ".gemini", "antigravity-acp", "acp_token.json")
	if err := os.MkdirAll(dirPath, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}

	if checkAgyAuthInDir(home) {
		t.Errorf("expected auth=false when cred path is a directory, got true")
	}
}

// TestAgy_FingerprintChangesWithCreds: latch fingerprint must change when creds change
func TestAgy_FingerprintChangesWithCreds(t *testing.T) {
	home, cleanup := tempHome(t)
	defer cleanup()
	acpPath := filepath.Join(home, ".gemini", "antigravity-acp", "acp_token.json")
	writeFile(t, acpPath, validAcpJSON)

	_, fp1 := agyAuthStateForPaths(agyAuthPaths(home, ""))
	if fp1 == "" {
		t.Fatalf("expected non-empty fingerprint for valid creds")
	}

	writeFile(t, acpPath, strings.Replace(validAcpJSON, `"refresh_token":"y"`, `"refresh_token":"z"`, 1))
	_, fp2 := agyAuthStateForPaths(agyAuthPaths(home, ""))
	if fp2 == "" {
		t.Fatalf("expected non-empty fingerprint after cred change")
	}
	if fp1 == fp2 {
		t.Errorf("fingerprint must change when credential content changes")
	}
}

// ---- production fetch path with fake child (no real agy, no browser, no OAuth) ----

const okUsageJSON = `{"status":"OK","command":{"data":{"groups":[{"name":"Gemini","displayName":"Gemini Models","buckets":[{"id":"gemini_5h","window":"5h","remaining_fraction":0.9,"reset_time":"2026-11-11T18:00:00Z"}]}]}}}`

// TestAgyFetch_MalformedCreds_NeverExecs: missing/malformed creds → child is never launched
func TestAgyFetch_MalformedCreds_NeverExecs(t *testing.T) {
	home := useFakeAgyHome(t)
	useFakeAgyCache(t)
	resetLatch()
	counter := filepath.Join(t.TempDir(), "counter.txt")
	useFakeAgyBin(t, counter, okUsageJSON, false)
	writeFile(t, filepath.Join(home, ".gemini", "antigravity-acp", "acp_token.json"), "garbage{{{")
	appDir := t.TempDir()

	res := FetchAgy(appDir, 10)
	if res.Status == "online" {
		t.Errorf("expected non-online status with malformed creds, got online")
	}
	if n := countExecs(t, counter); n != 0 {
		t.Errorf("child must never exec with malformed creds, ran %d times", n)
	}
}

// TestAgyFetch_AuthFailure_LatchesRepeatPolls: 1 auth failure → later polls do not exec
// until the credential fingerprint actually changes.
func TestAgyFetch_AuthFailure_LatchesRepeatPolls(t *testing.T) {
	home := useFakeAgyHome(t)
	useFakeAgyCache(t)
	resetLatch()
	counter := filepath.Join(t.TempDir(), "counter.txt")
	useFakeAgyBin(t, counter, "", true) // auth-failing child
	writeFile(t, filepath.Join(home, ".gemini", "antigravity-acp", "acp_token.json"), validAcpJSON)
	appDir := t.TempDir()

	res1 := FetchAgy(appDir, 10)
	if res1.Status == "online" {
		t.Fatalf("first fetch: expected failure status, got online")
	}
	if n := countExecs(t, counter); n != 1 {
		t.Fatalf("first fetch: child ran %d times, want 1", n)
	}

	// second, third poll with SAME credentials — child must NOT be relaunched
	res2 := FetchAgy(appDir, 10)
	res3 := FetchAgy(appDir, 10)
	if n := countExecs(t, counter); n != 1 {
		t.Errorf("auth failure must latch: child ran %d times total, want 1", n)
	}
	if res2.Status == "online" || res3.Status == "online" {
		t.Errorf("latched polls must not report online")
	}
}

// TestAgyFetch_AuthFailure_RelaunchAfterCredsChange: creds actually changed → exec again
func TestAgyFetch_AuthFailure_RelaunchAfterCredsChange(t *testing.T) {
	home := useFakeAgyHome(t)
	useFakeAgyCache(t)
	resetLatch()
	counter := filepath.Join(t.TempDir(), "counter.txt")
	useFakeAgyBin(t, counter, "", true)
	acpPath := filepath.Join(home, ".gemini", "antigravity-acp", "acp_token.json")
	writeFile(t, acpPath, validAcpJSON)
	appDir := t.TempDir()

	FetchAgy(appDir, 10)
	if n := countExecs(t, counter); n != 1 {
		t.Fatalf("child ran %d times, want 1", n)
	}

	// user re-logins: credentials change → next poll may retry once
	writeFile(t, acpPath, strings.Replace(validAcpJSON, `"refresh_token":"y"`, `"refresh_token":"new"`, 1))
	FetchAgy(appDir, 10)
	if n := countExecs(t, counter); n != 2 {
		t.Errorf("child ran %d times after cred change, want 2", n)
	}
}

// TestAgyFetch_AuthFailure_ExplicitRetryClearsLatch: force-refresh (--no-cache path)
// is the explicit user retry and must clear the latch.
func TestAgyFetch_AuthFailure_ExplicitRetryClearsLatch(t *testing.T) {
	home := useFakeAgyHome(t)
	useFakeAgyCache(t)
	resetLatch()
	counter := filepath.Join(t.TempDir(), "counter.txt")
	useFakeAgyBin(t, counter, "", true)
	writeFile(t, filepath.Join(home, ".gemini", "antigravity-acp", "acp_token.json"), validAcpJSON)
	appDir := t.TempDir()

	FetchAgy(appDir, 10)
	FetchAgy(appDir, 10)
	if n := countExecs(t, counter); n != 1 {
		t.Fatalf("precondition: child ran %d times, want 1", n)
	}

	SetAgyBypassCache(true) // explicit retry (GUI force refresh / CLI --no-cache)
	defer SetAgyBypassCache(false)
	FetchAgy(appDir, 10)
	if n := countExecs(t, counter); n != 2 {
		t.Errorf("explicit retry must clear latch: child ran %d times, want 2", n)
	}
}

// TestAgyFetch_Success_ClearsLatch: после auth-failure повтор возможен только
// при смене кредов; если agy тогда отвечает успехом — latch снимается.
func TestAgyFetch_Success_ClearsLatch(t *testing.T) {
	home := useFakeAgyHome(t)
	useFakeAgyCache(t)
	resetLatch()
	counter := filepath.Join(t.TempDir(), "counter.txt")
	useFakeAgyBin(t, counter, "", true)
	acpPath := filepath.Join(home, ".gemini", "antigravity-acp", "acp_token.json")
	writeFile(t, acpPath, validAcpJSON)
	appDir := t.TempDir()

	FetchAgy(appDir, 10) // auth failure → latch
	if agyAuthLatch.failed != true {
		t.Fatalf("precondition: latch must be engaged after auth failure")
	}

	// пользователь обновил креды → retry разрешён; agy теперь отвечает успехом
	writeFile(t, acpPath, strings.Replace(validAcpJSON, `"refresh_token":"y"`, `"refresh_token":"new"`, 1))
	useFakeAgyBin(t, counter, okUsageJSON, false)
	res := FetchAgy(appDir, 10)
	if res.Status != "online" {
		t.Fatalf("expected online after successful fetch, got %q", res.Status)
	}
	if agyAuthLatch.failed {
		t.Errorf("latch must be cleared after successful fetch")
	}
	if !agyBucketsHaveRealData(res.Buckets) {
		t.Errorf("expected real bucket data after successful fetch")
	}
}

// ---- placeholder semantics: no fabricated numbers ----

func bucketPlaceholder(m map[string]interface{}) bool {
	ph, _ := m["placeholder"].(bool)
	return ph
}

// TestAgy_UnknownBucket_IsPlaceholder: fallback buckets are marked placeholders,
// not "0% used" measured data.
func TestAgy_UnknownBucket_IsPlaceholder(t *testing.T) {
	b := defaultAgyBucket("unknown")
	if !bucketPlaceholder(b) {
		t.Errorf("defaultAgyBucket(unknown) must have placeholder=true, got %v", b["placeholder"])
	}
	if getStr(b, "level") != "unknown" {
		t.Errorf("defaultAgyBucket(unknown).level = %v, want 'unknown'", b["level"])
	}
}

// TestAgy_ParseBuckets_NilResponse_AllPlaceholder: nothing parsed → placeholders
func TestAgy_ParseBuckets_NilResponse_AllPlaceholder(t *testing.T) {
	buckets := ParseAgyBuckets(nil)
	for name, b := range map[string]map[string]interface{}{
		"Gemini5h": buckets.Gemini5h, "GeminiWeekly": buckets.GeminiWeekly,
		"Claude5h": buckets.Claude5h, "ClaudeWeekly": buckets.ClaudeWeekly,
	} {
		if !bucketPlaceholder(b) {
			t.Errorf("%s must be placeholder when response is nil", name)
		}
		if agyBucketsHaveRealData(AgyBuckets{Gemini5h: b}) {
			t.Errorf("%s must not count as real data when response is nil", name)
		}
	}
}

// TestAgy_ParseBuckets_EmptyGroups_AllPlaceholder: groups present but empty → placeholders
func TestAgy_ParseBuckets_EmptyGroups_AllPlaceholder(t *testing.T) {
	resp := map[string]interface{}{
		"command": map[string]interface{}{
			"data": map[string]interface{}{"groups": []interface{}{}},
		},
	}
	buckets := ParseAgyBuckets(resp)
	if agyBucketsHaveRealData(buckets) {
		t.Errorf("no real data expected when groups are empty")
	}
}

// TestAgy_ParseBuckets_MissingFraction_IsPlaceholder: a bucket without numeric
// remaining_fraction has no measured data → placeholder for that slot
func TestAgy_ParseBuckets_MissingFraction_IsPlaceholder(t *testing.T) {
	raw := `{"command":{"data":{"groups":[
		{"name":"Gemini","buckets":[{"id":"gemini_5h","window":"5h","reset_time":"2026-11-11T18:00:00Z"}]},
		{"name":"Claude","buckets":[{"id":"claude_5h","window":"5h","remaining_fraction":0.5,"reset_time":"2026-11-11T18:00:00Z"}]}
	]}}}`
	var resp map[string]interface{}
	if err := json.Unmarshal([]byte(raw), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	buckets := ParseAgyBuckets(resp)
	if !bucketPlaceholder(buckets.Gemini5h) {
		t.Errorf("Gemini5h without remaining_fraction must be placeholder")
	}
	if bucketPlaceholder(buckets.Claude5h) {
		t.Errorf("Claude5h with remaining_fraction must be real")
	}
}

// TestAgy_ParseBuckets_RealZeroUsed_NotPlaceholder: genuine 0% used stays 0% (real)
func TestAgy_ParseBuckets_RealZeroUsed_NotPlaceholder(t *testing.T) {
	raw := `{"command":{"data":{"groups":[
		{"name":"Gemini","buckets":[{"id":"gemini_5h","window":"5h","remaining_fraction":1.0,"reset_time":"2026-11-11T18:00:00Z"}]}
	]}}}`
	var resp map[string]interface{}
	if err := json.Unmarshal([]byte(raw), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	buckets := ParseAgyBuckets(resp)
	if bucketPlaceholder(buckets.Gemini5h) {
		t.Errorf("real remaining_fraction=1.0 (0%% used) must NOT be a placeholder")
	}
	if pct := getFloat(buckets.Gemini5h, "used_percent", -1); pct != 0.0 {
		t.Errorf("used_percent = %v, want 0.0", pct)
	}
}

// TestAgy_ParseBuckets_RealGroups: real API response with groups parses percentages
func TestAgy_ParseBuckets_RealGroups(t *testing.T) {
	raw := `{
		"command": {
			"data": {
				"groups": [
					{
						"name": "Gemini",
						"displayName": "Gemini Models",
						"buckets": [
							{"id": "gemini_5h", "window": "5h", "remaining_fraction": 0.857, "reset_time": "2026-11-11T18:00:00Z"},
							{"id": "gemini_weekly", "window": "weekly", "remaining_fraction": 0.357, "reset_time": "2026-11-15T00:00:00Z"}
						]
					},
					{
						"name": "Claude",
						"displayName": "Claude / 3P Models",
						"buckets": [
							{"id": "claude_5h", "window": "5h", "remaining_fraction": 0.75, "reset_time": "2026-11-11T18:00:00Z"},
							{"id": "claude_weekly", "window": "weekly", "remaining_fraction": 0.5, "reset_time": "2026-11-15T00:00:00Z"}
						]
					}
				]
			}
		}
	}`
	var resp map[string]interface{}
	if err := json.Unmarshal([]byte(raw), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	buckets := ParseAgyBuckets(resp)

	tests := []struct {
		name    string
		bucket  map[string]interface{}
		wantPct float64
	}{
		{"Gemini5h", buckets.Gemini5h, 85.7},
		{"GeminiWeekly", buckets.GeminiWeekly, 35.7},
		{"Claude5h", buckets.Claude5h, 75.0},
		{"ClaudeWeekly", buckets.ClaudeWeekly, 50.0},
	}
	for _, tc := range tests {
		if bucketPlaceholder(tc.bucket) {
			t.Errorf("%s: real parsed data must not be placeholder", tc.name)
		}
		pct, ok := tc.bucket["percent"].(float64)
		if !ok {
			t.Errorf("%s: percent not float64: %T %v", tc.name, tc.bucket["percent"], tc.bucket["percent"])
			continue
		}
		if pct != tc.wantPct {
			t.Errorf("%s: percent = %.1f, want %.1f", tc.name, pct, tc.wantPct)
		}
	}
}

// TestAgyFetch_FallbackPreservesCachedRealBuckets: при сбое опроса ранее
// сохранённые В КОРНЕ limits.json (data.agy.*) реальные числа показываются
// со статусом stale и НЕ стираются заглушками.
func TestAgyFetch_FallbackPreservesCachedRealBuckets(t *testing.T) {
	home := useFakeAgyHome(t)
	useFakeAgyCache(t)
	resetLatch()
	counter := filepath.Join(t.TempDir(), "counter.txt")
	useFakeAgyBin(t, counter, "", true) // auth-failing child
	writeFile(t, filepath.Join(home, ".gemini", "antigravity-acp", "acp_token.json"), validAcpJSON)
	appDir := t.TempDir()

	// имитируем прошлый успешный цикл: корневой limits.json с реальными бакетами
	prev := map[string]interface{}{
		"status":       "online",
		"last_updated": "2026-11-11 10:00:00",
		"data": map[string]interface{}{
			"agy": map[string]interface{}{
				"status":       "online",
				"last_updated": "2026-11-11 10:00:00",
				"gemini_5h": map[string]interface{}{
					"percent": 90.0, "fraction": 0.9,
					"used_percent": 10.0, "used_fraction": 0.1,
					"reset_in": "2h", "reset_time": "2026-11-11T14:00:00Z", "level": "ok",
				},
				"claude_weekly": map[string]interface{}{
					"percent": 50.0, "fraction": 0.5,
					"used_percent": 50.0, "used_fraction": 0.5,
					"reset_in": "3d", "reset_time": "2026-11-15T00:00:00Z", "level": "warn",
				},
			},
		},
	}
	b, _ := json.Marshal(prev)
	writeFile(t, filepath.Join(appDir, "limits.json"), string(b))

	res := FetchAgy(appDir, 10)
	if res.Status != "stale" {
		t.Errorf("expected stale status when previous success exists, got %q", res.Status)
	}
	if bucketPlaceholder(res.Buckets.Gemini5h) {
		t.Errorf("cached real gemini_5h bucket must be preserved, not replaced by placeholder")
	}
	if pct := getFloat(res.Buckets.Gemini5h, "percent", -1); pct != 90.0 {
		t.Errorf("preserved gemini_5h percent = %v, want 90.0", pct)
	}
	if bucketPlaceholder(res.Buckets.ClaudeWeekly) {
		t.Errorf("cached real claude_weekly bucket must be preserved")
	}
	// бакетов gemini_weekly/claude_5h в кэше не было — остаются заглушки
	if !bucketPlaceholder(res.Buckets.GeminiWeekly) {
		t.Errorf("gemini_weekly was never cached and must stay placeholder")
	}
}

// TestAgy_BucketFromMap_PlaceholderPropagation: model layer keeps the placeholder flag
func TestAgy_BucketFromMap_PlaceholderPropagation(t *testing.T) {
	ph := bucketFromMap(defaultAgyBucket("unknown"))
	if !ph.Placeholder {
		t.Errorf("bucketFromMap(unknown bucket) must set Placeholder=true")
	}
	real := bucketFromMap(defaultAgyBucket("ok"))
	if real.Placeholder {
		t.Errorf("bucketFromMap(ok bucket) must not be placeholder")
	}
}
