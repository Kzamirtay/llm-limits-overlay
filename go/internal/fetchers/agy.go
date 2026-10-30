package fetchers

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// ---- кэш /usage (300с, память + диск, формат совместим с C#-версией) ----

const agyCacheTTL = 300.0

type agyCacheRecord struct {
	Timestamp float64                `json:"timestamp"`
	Raw       map[string]interface{} `json:"raw"`
	Data      map[string]interface{} `json:"data"`
}

var (
	agyMemCache    *agyCacheRecord
	agyBypassCache bool
)

func SetAgyBypassCache(v bool) { agyBypassCache = v }

func agyCachePath() string {
	return filepath.Join(os.TempDir(), "llm_limits_agy_usage_cache.json")
}

func loadAgyCache() *agyCacheRecord {
	if agyBypassCache {
		return nil
	}
	now := float64(time.Now().Unix())
	if agyMemCache != nil && now-agyMemCache.Timestamp >= 0 && now-agyMemCache.Timestamp < agyCacheTTL {
		return agyMemCache
	}
	raw, err := os.ReadFile(agyCachePath())
	if err != nil {
		return nil
	}
	var rec agyCacheRecord
	if err := json.Unmarshal(raw, &rec); err != nil || rec.Data == nil {
		return nil
	}
	if now-rec.Timestamp >= 0 && now-rec.Timestamp < agyCacheTTL {
		agyMemCache = &rec
		return &rec
	}
	return nil
}

func saveAgyCache(raw, data map[string]interface{}) {
	rec := &agyCacheRecord{Timestamp: float64(time.Now().Unix()), Raw: raw, Data: data}
	agyMemCache = rec
	b, err := json.MarshalIndent(rec, "", "  ")
	if err == nil {
		tmp := agyCachePath() + ".tmp"
		if os.WriteFile(tmp, b, 0o644) == nil {
			os.Rename(tmp, agyCachePath())
		}
	}
}

// ---- резолвер бинарника и токена ----

func FindAgyBinary() string {
	if envBin := os.Getenv("LLM_LIMITS_AGY_BIN"); envBin != "" {
		if _, err := os.Stat(envBin); err == nil {
			return envBin
		}
	}

	// PATH
	sep := ":"
	if runtime.GOOS == "windows" {
		sep = ";"
	}
	for _, dir := range strings.Split(os.Getenv("PATH"), sep) {
		dir = strings.TrimSpace(dir)
		if dir == "" {
			continue
		}
		if runtime.GOOS == "windows" {
			if _, err := os.Stat(filepath.Join(dir, "agy.exe")); err == nil {
				return filepath.Join(dir, "agy.exe")
			}
		}
		if _, err := os.Stat(filepath.Join(dir, "agy")); err == nil {
			return filepath.Join(dir, "agy")
		}
	}

	if runtime.GOOS == "windows" {
		if la := os.Getenv("LOCALAPPDATA"); la != "" {
			if _, err := os.Stat(filepath.Join(la, "agy", "bin", "agy.exe")); err == nil {
				return filepath.Join(la, "agy", "bin", "agy.exe")
			}
		}
		if up := userHome(); up != "" {
			if _, err := os.Stat(filepath.Join(up, "AppData", "Local", "agy", "bin", "agy.exe")); err == nil {
				return filepath.Join(up, "AppData", "Local", "agy", "bin", "agy.exe")
			}
		}
		return ""
	}

	// Linux
	if home := userHome(); home != "" {
		if _, err := os.Stat(filepath.Join(home, ".local", "bin", "agy")); err == nil {
			return filepath.Join(home, ".local", "bin", "agy")
		}
	}
	if _, err := os.Stat("/usr/local/bin/agy"); err == nil {
		return "/usr/local/bin/agy"
	}
	return ""
}

func userHome() string {
	if h, err := os.UserHomeDir(); err == nil {
		return h
	}
	return ""
}

// CheckAgyAuth — токен-файл существует и не пуст (блок OAuth-попапа).
func CheckAgyAuth() bool {
	home := userHome()
	if runtime.GOOS == "windows" && home != "" {
		if fileNonEmpty(filepath.Join(home, ".gemini", "antigravity-cli", "antigravity-oauth-token")) {
			return true
		}
		if la := os.Getenv("LOCALAPPDATA"); la != "" {
			if fileNonEmpty(filepath.Join(la, ".gemini", "antigravity-cli", "antigravity-oauth-token")) {
				return true
			}
		}
		return false
	}
	if home != "" {
		return fileNonEmpty(filepath.Join(home, ".gemini", "antigravity-cli", "antigravity-oauth-token"))
	}
	return false
}

func fileNonEmpty(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && fi.Size() > 0
}

// ---- парсинг бакетов ----

// AgyBuckets — 4 бакета аги.
type AgyBuckets struct {
	Gemini5h     map[string]interface{}
	GeminiWeekly map[string]interface{}
	Claude5h     map[string]interface{}
	ClaudeWeekly map[string]interface{}
}

// ParseAgyBuckets — groups[] -> бакеты (1:1 с C# ParseQuotaData).
func ParseAgyBuckets(resp map[string]interface{}) AgyBuckets {
	out := AgyBuckets{
		Gemini5h:     defaultAgyBucket("ok"),
		GeminiWeekly: defaultAgyBucket("ok"),
		Claude5h:     defaultAgyBucket("ok"),
		ClaudeWeekly: defaultAgyBucket("ok"),
	}
	if resp == nil {
		return out
	}
	dataObj := resp
	if cmd := getMap(resp, "command"); cmd != nil && getMap(cmd, "data") != nil {
		dataObj = getMap(cmd, "data")
	} else if getMap(resp, "response") != nil {
		dataObj = getMap(resp, "response")
	}

	for _, gObj := range getList(dataObj, "groups") {
		group, ok := gObj.(map[string]interface{})
		if !ok {
			continue
		}
		gname := strings.ToLower(getStr(group, "name") + " " + getStr(group, "displayName"))
		for _, bObj := range getList(group, "buckets") {
			b, ok := bObj.(map[string]interface{})
			if !ok {
				continue
			}
			bid := strings.ToLower(getStr(b, "id") + " " + getStr(b, "bucketId"))
			window := strings.ToLower(getStr(b, "window"))

			frac := 1.0
			if hasNonNull(b, "remaining_fraction") {
				frac = getFloat(b, "remaining_fraction", 1.0)
			} else if hasNonNull(b, "remainingFraction") {
				frac = getFloat(b, "remainingFraction", 1.0)
			}
			resetTime := getStr(b, "reset_time")
			if resetTime == "" {
				resetTime = getStr(b, "resetTime")
			}
			pct := round1(frac * 100)
			usedFrac := clamp01(round4(1.0 - frac))
			usedPct := round1(usedFrac * 100)
			display := getStr(b, "name")
			if display == "" {
				display = getStr(b, "displayName")
			}
			if display == "" {
				display = bid
			}
			info := map[string]interface{}{
				"percent":     pct,
				"fraction":    frac,
				"used_percent":  usedPct,
				"used_fraction": usedFrac,
				"reset_in":    FormatTimeDiff(resetTime, "N/A"),
				"reset_time":  resetTime,
				"level":       GetUsedStatusLevel(usedFrac),
				"displayName": display,
				"description": getStr(b, "description"),
			}

			isGemini := strings.Contains(gname, "gemini")
			isClaude := strings.Contains(gname, "claude") || strings.Contains(gname, "gpt") || strings.Contains(gname, "3p")
			if !isGemini && !isClaude {
				isGemini = strings.Contains(bid, "gemini")
				isClaude = strings.Contains(bid, "3p") || strings.Contains(bid, "claude") || strings.Contains(bid, "gpt")
			}
			if isGemini {
				if window == "5h" || strings.Contains(bid, "5h") {
					out.Gemini5h = info
				} else if window == "weekly" || strings.Contains(bid, "weekly") {
					out.GeminiWeekly = info
				}
			} else if isClaude {
				if window == "5h" || strings.Contains(bid, "5h") {
					out.Claude5h = info
				} else if window == "weekly" || strings.Contains(bid, "weekly") {
					out.ClaudeWeekly = info
				}
			}
		}
	}
	return out
}

func defaultAgyBucket(level string) map[string]interface{} {
	return map[string]interface{}{
		"percent": 100.0, "fraction": 1.0,
		"used_percent": 0.0, "used_fraction": 0.0,
		"reset_in": "N/A", "reset_time": "", "level": level,
	}
}

func round1(f float64) float64 {
	return float64(int(f*10+0.5)) / 10
}

func round4(f float64) float64 {
	return float64(int(f*10000+0.5)) / 10000
}

func clamp01(f float64) float64 {
	if f < 0 {
		return 0
	}
	if f > 1 {
		return 1
	}
	return f
}

// ---- собственно фетч agy ----

// AgyResult — результат аги-воркера.
type AgyResult struct {
	Status      string
	LastUpdated string
	Buckets     AgyBuckets
	Raw         map[string]interface{}
	CacheHit    bool
}

// FetchAgy — кэш -> бинарник -> токен -> exec. appDir для лога.
func FetchAgy(appDir string, timeoutSec int) *AgyResult {
	nowIso := NowIso()

	if rec := loadAgyCache(); rec != nil {
		return &AgyResult{
			Status: "online", LastUpdated: cacheTsToIso(rec.Timestamp),
			Buckets: ParseAgyBuckets(rec.Data), Raw: rec.Raw, CacheHit: true,
		}
	}

	bin := FindAgyBinary()
	if bin == "" {
		LogLine(appDir, "[agy] missing agy (not found in LLM_LIMITS_AGY_BIN, PATH, or AppData)")
		return fallbackAgy(appDir, nowIso, "missing agy")
	}
	if !CheckAgyAuth() {
		LogLine(appDir, "[agy] Token file missing or empty; blocking interactive OAuth popup.")
		return fallbackAgy(appDir, nowIso, "auth required: agy not logged in (run 'agy auth login')")
	}

	LogLine(appDir, "[live] running "+bin+" -p /usage --output-format json...")
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(timeoutSec)*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, "-p", "/usage", "--output-format", "json")
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	if ctx.Err() == context.DeadlineExceeded {
		LogLine(appDir, fmt.Sprintf("[agy] timed out after %ds executing agy", timeoutSec))
		return fallbackAgy(appDir, nowIso, fmt.Sprintf("timeout after %ds executing agy", timeoutSec))
	}
	if err != nil {
		combined := strings.ToLower(stdout.String() + " " + stderr.String())
		msg := fmt.Sprintf("agy failed: %s", err.Error())
		if strings.Contains(combined, "auth") || strings.Contains(combined, "login") || strings.Contains(combined, "unauthorized") {
			msg = "auth required"
		}
		LogLine(appDir, "[agy] "+msg)
		return fallbackAgy(appDir, nowIso, msg)
	}

	parsed := extractLastJson(stdout.String())
	if parsed == nil {
		LogLine(appDir, "[agy] parse failed: invalid JSON output from agy")
		return fallbackAgy(appDir, nowIso, "parse failed: invalid JSON output from agy")
	}
	statusStr := strings.ToUpper(getStr(parsed, "status"))
	if statusStr == "ERROR" || statusStr == "FAILED" {
		respText := getStr(parsed, "response")
		msg := "agy error: unknown"
		if strings.Contains(strings.ToLower(respText), "auth") || strings.Contains(strings.ToLower(respText), "login") {
			msg = "auth required"
		} else if respText != "" {
			msg = "agy error: " + respText
		}
		LogLine(appDir, "[agy] "+msg)
		return fallbackAgy(appDir, nowIso, msg)
	}

	cmdData := getMap(parsed, "command")
	var inner map[string]interface{}
	if cmdData != nil && getMap(cmdData, "data") != nil {
		inner = getMap(cmdData, "data")
	} else if getMap(parsed, "response") != nil {
		inner = getMap(parsed, "response")
	} else {
		inner = parsed
	}
	saveAgyCache(parsed, inner)

	return &AgyResult{
		Status: "online", LastUpdated: nowIso,
		Buckets: ParseAgyBuckets(inner), Raw: parsed,
	}
}

func cacheTsToIso(ts float64) string {
	return time.Unix(int64(ts), 0).Local().Format("2006-01-02 15:04:05")
}

// extractLastJson — последняя строка, целиком являющаяся JSON-объектом
// (как в C#: сначала построчно, fallback — последняя {...}-подстрока).
func extractLastJson(text string) map[string]interface{} {
	lines := strings.Split(text, "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		line := strings.TrimSpace(strings.TrimRight(lines[i], "\r"))
		if len(line) < 2 || line[0] != '{' || line[len(line)-1] != '}' {
			continue
		}
		var m map[string]interface{}
		if err := json.Unmarshal([]byte(line), &m); err == nil {
			return m
		}
	}
	start := strings.LastIndex(text, "{")
	end := strings.LastIndex(text, "}")
	if start == -1 || end == -1 || end <= start {
		return nil
	}
	var m map[string]interface{}
	if err := json.Unmarshal([]byte(text[start:end+1]), &m); err != nil {
		return nil
	}
	return m
}

// fallbackAgy — stale/error из кэшированного провайдер-блока/корня.
func fallbackAgy(appDir, nowIso, errMsg string) *AgyResult {
	// прежнее успешное время из корневого limits.json (data.agy.last_updated)
	prevSuccess := ""
	if root := loadLimitsJson(appDir); root != nil {
		if data := getMap(root, "data"); data != nil {
			if agyMeta := getMap(data, "agy"); agyMeta != nil {
				prevSuccess = getStr(agyMeta, "last_updated")
			}
		}
	}
	lastUpd := prevSuccess
	if lastUpd == "" {
		lastUpd = nowIso
	}
	buckets := defaultAgyBuckets("unknown")
	// если есть фрагмент/корень с бакетами — берём их
	if frag := loadCachedProviderBlocks(appDir, "agy"); frag != nil {
		b := agyBucketsFromBlock(frag)
		if b != nil {
			buckets = *b
		}
	}
	status := "error"
	if prevSuccess != "" {
		status = "stale"
	}
	return &AgyResult{Status: status, LastUpdated: lastUpd, Buckets: buckets}
}

func defaultAgyBuckets(level string) AgyBuckets {
	return AgyBuckets{
		Gemini5h:     defaultAgyBucket(level),
		GeminiWeekly: defaultAgyBucket(level),
		Claude5h:     defaultAgyBucket(level),
		ClaudeWeekly: defaultAgyBucket(level),
	}
}

func agyBucketsFromBlock(block map[string]interface{}) *AgyBuckets {
	src := block
	if b, ok := block["buckets"].(map[string]interface{}); ok {
		src = b
	}
	pick := func(key string) map[string]interface{} {
		if v, ok := src[key].(map[string]interface{}); ok {
			return v
		}
		return nil
	}
	g5, gw, c5, cw := pick("gemini_5h"), pick("gemini_weekly"), pick("claude_5h"), pick("claude_weekly")
	if g5 == nil && gw == nil && c5 == nil && cw == nil {
		return nil
	}
	out := defaultAgyBuckets("unknown")
	if g5 != nil {
		out.Gemini5h = g5
	}
	if gw != nil {
		out.GeminiWeekly = gw
	}
	if c5 != nil {
		out.Claude5h = c5
	}
	if cw != nil {
		out.ClaudeWeekly = cw
	}
	return &out
}
