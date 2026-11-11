package fetchers

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// ---- helpers ----

// tempHome creates a temp directory tree simulating a Windows user home.
// Returns the home dir and a cleanup function.
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

// checkAgyAuthInDir calls checkAgyAuthPaths with explicit paths — avoids relying on real OS HOME.
// Wraps the internal path-list logic without touching the filesystem outside tmp.
func checkAgyAuthInDir(home string) bool {
	paths := agyAuthPaths(home, "")
	for _, p := range paths {
		if fileNonEmpty(p) {
			return true
		}
	}
	return false
}

// ---- TestAgy_AcpTokenOnly: acp_token.json present (AGY 1.5.4 real cred file) ----
// RED until agyAuthPaths includes the acp path.
func TestAgy_AcpTokenOnly(t *testing.T) {
	home, cleanup := tempHome(t)
	defer cleanup()

	acpPath := filepath.Join(home, ".gemini", "antigravity-acp", "acp_token.json")
	writeFile(t, acpPath, `{"client_id":"x","refresh_token":"y","token_uri":"https://oauth2.googleapis.com/token","scopes":["aicode"],"project_id":"aicode-consumers"}`)

	if !checkAgyAuthInDir(home) {
		t.Errorf("expected auth=true when acp_token.json present, got false")
	}
}

// ---- TestAgy_OauthCredsOnly: oauth_creds.json present (legacy path in 1dccb89 fix) ----
func TestAgy_OauthCredsOnly(t *testing.T) {
	home, cleanup := tempHome(t)
	defer cleanup()

	oauthPath := filepath.Join(home, ".gemini", "oauth_creds.json")
	writeFile(t, oauthPath, `{"access_token":"tok","refresh_token":"rtok","scope":"cloud-platform","token_type":"Bearer","id_token":"idtok","expiry_date":9999999999999}`)

	if !checkAgyAuthInDir(home) {
		t.Errorf("expected auth=true when oauth_creds.json present, got false")
	}
}

// ---- TestAgy_LegacyTokenOnly: antigravity-oauth-token present ----
func TestAgy_LegacyTokenOnly(t *testing.T) {
	home, cleanup := tempHome(t)
	defer cleanup()

	legacyPath := filepath.Join(home, ".gemini", "antigravity-cli", "antigravity-oauth-token")
	writeFile(t, legacyPath, "some-legacy-token-value")

	if !checkAgyAuthInDir(home) {
		t.Errorf("expected auth=true when legacy antigravity-oauth-token present, got false")
	}
}

// ---- TestAgy_NoCreds: no credential files → blocked ----
func TestAgy_NoCreds(t *testing.T) {
	home, cleanup := tempHome(t)
	defer cleanup()

	if checkAgyAuthInDir(home) {
		t.Errorf("expected auth=false when no cred files present, got true")
	}
}

// ---- TestAgy_EmptyFile: empty file → blocked ----
func TestAgy_EmptyFile(t *testing.T) {
	home, cleanup := tempHome(t)
	defer cleanup()

	acpPath := filepath.Join(home, ".gemini", "antigravity-acp", "acp_token.json")
	writeFile(t, acpPath, "") // empty

	if checkAgyAuthInDir(home) {
		t.Errorf("expected auth=false when cred file is empty, got true")
	}
}

// ---- TestAgy_MalformedJson: malformed JSON → still OK (we only check non-empty, not valid JSON) ----
// The auth guard only verifies file exists and is non-empty to avoid OAuth popup.
// Validation of token validity is left to the agy binary itself.
func TestAgy_MalformedJson_NonEmpty(t *testing.T) {
	home, cleanup := tempHome(t)
	defer cleanup()

	acpPath := filepath.Join(home, ".gemini", "antigravity-acp", "acp_token.json")
	writeFile(t, acpPath, "not valid json") // non-empty but malformed

	// We allow this — file exists and is non-empty, so we let agy CLI try.
	// If token is invalid, agy CLI returns error without popup.
	if !checkAgyAuthInDir(home) {
		t.Errorf("expected auth=true (non-empty file allowed), got false")
	}
}

// ---- TestAgy_UnknownBucketLevel: unknown bucket → level must be 'unknown', percent 100 ----
// Guards against stale unknown being displayed as "0% used" or "100% remaining measured data"
func TestAgy_UnknownBucketLevel(t *testing.T) {
	b := defaultAgyBucket("unknown")
	level, ok := b["level"].(string)
	if !ok || level != "unknown" {
		t.Errorf("defaultAgyBucket(unknown).level = %v, want 'unknown'", b["level"])
	}
	// percent should be 100.0 (full remaining) but level=unknown means display as placeholder
	pct, ok := b["percent"].(float64)
	if !ok || pct != 100.0 {
		t.Errorf("defaultAgyBucket(unknown).percent = %v, want 100.0", b["percent"])
	}
}

// ---- TestAgy_ParseBuckets_AllUnknownWhenNoGroups: empty response → unknown ----
func TestAgy_ParseBuckets_AllUnknownWhenNoGroups(t *testing.T) {
	// ParseAgyBuckets with nil response returns "ok" defaults (not unknown).
	// This tests the defaultAgyBuckets("unknown") path used in fallbackAgy.
	buckets := defaultAgyBuckets("unknown")
	for name, b := range map[string]map[string]interface{}{
		"Gemini5h":     buckets.Gemini5h,
		"GeminiWeekly": buckets.GeminiWeekly,
		"Claude5h":     buckets.Claude5h,
		"ClaudeWeekly": buckets.ClaudeWeekly,
	} {
		level, _ := b["level"].(string)
		if level != "unknown" {
			t.Errorf("%s.level = %q, want 'unknown'", name, level)
		}
	}
}

// ---- TestAgy_ParseBuckets_RealGroups: real API response with groups ----
func TestAgy_ParseBuckets_RealGroups(t *testing.T) {
	raw := `{
		"command": {
			"data": {
				"groups": [
					{
						"name": "Gemini",
						"displayName": "Gemini Models",
						"buckets": [
							{
								"id": "gemini_5h",
								"window": "5h",
								"remaining_fraction": 0.857,
								"reset_time": "2026-11-11T18:00:00Z"
							},
							{
								"id": "gemini_weekly",
								"window": "weekly",
								"remaining_fraction": 0.357,
								"reset_time": "2026-11-15T00:00:00Z"
							}
						]
					},
					{
						"name": "Claude",
						"displayName": "Claude / 3P Models",
						"buckets": [
							{
								"id": "claude_5h",
								"window": "5h",
								"remaining_fraction": 0.75,
								"reset_time": "2026-11-11T18:00:00Z"
							},
							{
								"id": "claude_weekly",
								"window": "weekly",
								"remaining_fraction": 0.5,
								"reset_time": "2026-11-15T00:00:00Z"
							}
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
		pct, ok := tc.bucket["percent"].(float64)
		if !ok {
			t.Errorf("%s: percent not float64: %T %v", tc.name, tc.bucket["percent"], tc.bucket["percent"])
			continue
		}
		if pct != tc.wantPct {
			t.Errorf("%s: percent = %.1f, want %.1f", tc.name, pct, tc.wantPct)
		}
		level, _ := tc.bucket["level"].(string)
		if level == "unknown" {
			t.Errorf("%s: level should not be 'unknown' for real parsed data", tc.name)
		}
	}
}
