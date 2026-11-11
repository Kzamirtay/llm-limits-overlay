package fetchers

import (
	"context"
	"crypto/sha256"
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

// SetAgyBypassCache — форсирует живой запуск agy (CLI --no-cache, GUI force
// refresh). Это же ЯВНЫЙ retry пользователя после auth-failure: снимаем latch.
func SetAgyBypassCache(v bool) {
	agyBypassCache = v
	if v {
		resetAgyAuthLatch()
	}
}

func agyCachePath() string {
	if agyCacheFileOverride != "" {
		return agyCacheFileOverride
	}
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

// agyAuthPaths returns the list of credential file paths to check for a given
// home dir and optional localAppData dir (Windows %LOCALAPPDATA%). Testable
// without touching the real filesystem.
//
// Credential files accepted (any one present and non-empty → auth OK):
//   - ~/.gemini/antigravity-acp/acp_token.json  (AGY 1.5.4 — primary)
//   - ~/.gemini/oauth_creds.json                (AGY CLI OAuth credential)
//   - ~/.gemini/antigravity-cli/antigravity-oauth-token  (legacy / pre-1.5)
//   - %LOCALAPPDATA%/.gemini/antigravity-acp/acp_token.json (Windows alt)
//   - %LOCALAPPDATA%/.gemini/oauth_creds.json              (Windows alt)
//   - %LOCALAPPDATA%/.gemini/antigravity-cli/antigravity-oauth-token (Windows alt)
func agyAuthPaths(home, localAppData string) []string {
	var paths []string
	if home != "" {
		paths = append(paths,
			filepath.Join(home, ".gemini", "antigravity-acp", "acp_token.json"),
			filepath.Join(home, ".gemini", "oauth_creds.json"),
			filepath.Join(home, ".gemini", "antigravity-cli", "antigravity-oauth-token"),
		)
	}
	if localAppData != "" {
		paths = append(paths,
			filepath.Join(localAppData, ".gemini", "antigravity-acp", "acp_token.json"),
			filepath.Join(localAppData, ".gemini", "oauth_creds.json"),
			filepath.Join(localAppData, ".gemini", "antigravity-cli", "antigravity-oauth-token"),
		)
	}
	return paths
}

// CheckAgyAuth — есть ли структурно валидные креды agy (read-only проверка).
// Файл должен существовать, быть не директорией, непустым и, для JSON-файлов,
// содержать обязательные непустые поля. Наличие валидного refresh_token не
// гарантирует, что токен не отозван — окончательную проверку делает сам agy.
func CheckAgyAuth() bool {
	ok, _ := agyAuthState()
	return ok
}

// agyAuthState находит первый структурно валидный файл кредов.
// Возвращает ok=false, когда креды отсутствуют/пустые/битые/нерелевантные, и
// fingerprint = "<path>:<sha256>" принятого файла — меняется при реальном
// изменении кредов (основа latch-защиты от повторных OAuth-запусков).
func agyAuthState() (bool, string) {
	home := agyHomeOverride
	if home == "" {
		home = userHome()
	}
	la := ""
	if runtime.GOOS == "windows" {
		la = os.Getenv("LOCALAPPDATA")
	}
	return agyAuthStateForPaths(agyAuthPaths(home, la))
}

// agyHomeOverride / agyCacheFileOverride — точки внедрения для тестов:
// позволяют проверять production-путь без касания реального HOME и общего кэша.
var agyHomeOverride string
var agyCacheFileOverride string

func agyAuthStateForPaths(paths []string) (bool, string) {
	for _, p := range paths {
		if validAgyCredFile(p) {
			return true, agyCredFingerprint(p)
		}
	}
	return false, ""
}

// validAgyCredFile — структурная read-only валидация одного файла кредов.
func validAgyCredFile(p string) bool {
	fi, err := os.Stat(p)
	if err != nil || fi.IsDir() || fi.Size() == 0 {
		return false
	}
	base := filepath.Base(p)
	switch base {
	case "acp_token.json":
		return validAcpCreds(p)
	case "oauth_creds.json":
		return validOauthCreds(p)
	default:
		// antigravity-oauth-token (legacy) — непрозрачный файл, не JSON:
		// проверяем только непустоту (историческое поведение).
		return true
	}
}

// validAcpCreds: AGY 1.5.4 ACP credentials — JSON-объект с обязательными
// непустыми полями (проверено против реального acp_token.json: root keys
// client_id, client_secret, refresh_token, token_uri, scopes, project_id).
func validAcpCreds(p string) bool {
	m, ok := readJSONObj(p)
	if !ok {
		return false
	}
	for _, k := range []string{"client_id", "refresh_token", "token_uri", "project_id"} {
		if strings.TrimSpace(getStr(m, k)) == "" {
			return false
		}
	}
	// scopes: непустой массив или непустая строка
	switch v := m["scopes"].(type) {
	case []interface{}:
		if len(v) == 0 {
			return false
		}
	case string:
		if strings.TrimSpace(v) == "" {
			return false
		}
	default:
		return false
	}
	return true
}

// validOauthCreds: OAuth credentials (Gemini CLI формат). Релевантны для AGY
// только с непустым refresh_token — без него silent re-auth невозможен.
func validOauthCreds(p string) bool {
	m, ok := readJSONObj(p)
	if !ok {
		return false
	}
	return strings.TrimSpace(getStr(m, "refresh_token")) != ""
}

// readJSONObj — прочитать файл как JSON-объект (read-only).
func readJSONObj(p string) (map[string]interface{}, bool) {
	b, err := os.ReadFile(p)
	if err != nil || len(b) == 0 {
		return nil, false
	}
	var m map[string]interface{}
	if err := json.Unmarshal(b, &m); err != nil || m == nil {
		return nil, false
	}
	return m, true
}

// agyCredFingerprint — путь + sha256 содержимого; в логах/отчётах не печатаем
// само содержимое (секреты), только необратимый хэш.
func agyCredFingerprint(p string) string {
	b, err := os.ReadFile(p)
	if err != nil {
		return p
	}
	sum := sha256.Sum256(b)
	return p + ":" + fmt.Sprintf("%x", sum[:8])
}

// agyAuthLatch — защита от ПОВТОРНЫХ запусков agy после auth-failure:
// при ошибке авторизации с фиксированным fingerprint кредов последующие
// минутные опросы НЕ запускают agy, пока креды реально не изменятся или
// пользователь явно не запросит retry (force refresh / --no-cache).
// Это НЕ cooldown: повторный запуск возможен только при смене fingerprint.
var agyAuthLatch struct {
	failed      bool
	fingerprint string
	logged      bool
}

func resetAgyAuthLatch() {
	agyAuthLatch.failed = false
	agyAuthLatch.fingerprint = ""
	agyAuthLatch.logged = false
}

// ---- парсинг бакетов ----

// AgyBuckets — 4 бакета аги.
type AgyBuckets struct {
	Gemini5h     map[string]interface{}
	GeminiWeekly map[string]interface{}
	Claude5h     map[string]interface{}
	ClaudeWeekly map[string]interface{}
}

// ParseAgyBuckets — groups[] -> бакеты квот. Бакет без числового
// remaining_fraction НЕ считается измеренными данными: слот остаётся
// заглушкой (placeholder=true), а не выдуманным «0%% использовано».
func ParseAgyBuckets(resp map[string]interface{}) AgyBuckets {
	out := defaultAgyBuckets("unknown")
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

			// без числового remaining_fraction данных о квоте нет —
			// оставляем заглушку, не выдумываем 100%% остатка
			var frac float64
			hasFrac := false
			if hasNonNull(b, "remaining_fraction") {
				frac = getFloat(b, "remaining_fraction", -1)
				hasFrac = frac >= 0
			} else if hasNonNull(b, "remainingFraction") {
				frac = getFloat(b, "remainingFraction", -1)
				hasFrac = frac >= 0
			}
			if !hasFrac {
				continue
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
				"percent":       pct,
				"fraction":      frac,
				"used_percent":  usedPct,
				"used_fraction": usedFrac,
				"reset_in":      FormatTimeDiff(resetTime, "N/A"),
				"reset_time":    resetTime,
				"level":         GetUsedStatusLevel(usedFrac),
				"displayName":   display,
				"description":   getStr(b, "description"),
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
	m := map[string]interface{}{
		"percent": 100.0, "fraction": 1.0,
		"used_percent": 0.0, "used_fraction": 0.0,
		"reset_in": "N/A", "reset_time": "", "level": level,
	}
	if level == "unknown" {
		// Явная заглушка: числовые поля сохраняют форму JSON, но
		// placeholder=true запрещает слою отображения рисовать «0%%» как факт.
		m["placeholder"] = true
	}
	return m
}

// agyBucketsHaveRealData — true, если есть хотя бы один бакет с реальными
// измеренными данными (не заглушка). Провайдер без ни одного валидного
// бакета не должен показываться как online.
func agyBucketsHaveRealData(b AgyBuckets) bool {
	for _, m := range []map[string]interface{}{
		b.Gemini5h, b.GeminiWeekly, b.Claude5h, b.ClaudeWeekly,
	} {
		if m == nil {
			continue
		}
		if ph, ok := m["placeholder"].(bool); ok && ph {
			continue
		}
		if getStr(m, "level") == "unknown" {
			continue
		}
		return true
	}
	return false
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

// FetchAgy — кэш -> бинарник -> валидация кредов -> exec (с latch-защитой).
// appDir для лога.
func FetchAgy(appDir string, timeoutSec int) *AgyResult {
	nowIso := NowIso()

	if rec := loadAgyCache(); rec != nil {
		buckets := ParseAgyBuckets(rec.Data)
		if agyBucketsHaveRealData(buckets) {
			return &AgyResult{
				Status: "online", LastUpdated: cacheTsToIso(rec.Timestamp),
				Buckets: buckets, Raw: rec.Raw, CacheHit: true,
			}
		}
		// кэш без валидных бакетов — не выдаём за online, идём за живыми данными
	}

	bin := FindAgyBinary()
	if bin == "" {
		LogLine(appDir, "[agy] missing agy (not found in LLM_LIMITS_AGY_BIN, PATH, or AppData)")
		return fallbackAgy(appDir, nowIso, "missing agy")
	}
	authOK, credFp := agyAuthState()
	if !authOK {
		// креды пропали/битые — exec не выполняется вообще (нет OAuth-риска)
		resetAgyAuthLatch()
		LogLine(appDir, "[agy] credentials missing, empty, malformed or irrelevant; NOT launching agy (blocks interactive OAuth).")
		return fallbackAgy(appDir, nowIso, "auth required: agy credentials missing or invalid (run 'agy auth login')")
	}
	if agyAuthLatch.failed && agyAuthLatch.fingerprint == credFp {
		// auth-failure с тем же fingerprint кредов: НЕ запускаем agy повторно.
		// Освобождение: реальное изменение кредов или явный retry (SetAgyBypassCache).
		if !agyAuthLatch.logged {
			LogLine(appDir, "[agy] auth failure latched for current credentials; not relaunching agy until credentials change or explicit retry.")
			agyAuthLatch.logged = true
		}
		return fallbackAgy(appDir, nowIso, "agy auth failed (latched; awaiting credential change or explicit retry)")
	}

	LogLine(appDir, "[live] running "+bin+" -p /usage --output-format json...")
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(timeoutSec)*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, "-p", "/usage", "--output-format", "json")
	hideConsole(cmd)
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
			agyAuthLatch.failed = true
			agyAuthLatch.fingerprint = credFp
			agyAuthLatch.logged = false
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
			agyAuthLatch.failed = true
			agyAuthLatch.fingerprint = credFp
			agyAuthLatch.logged = false
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
	buckets := ParseAgyBuckets(inner)
	if !agyBucketsHaveRealData(buckets) {
		// валидный JSON, но нет ни одного измеренного бакета — не online
		msg := "agy returned no quota data"
		LogLine(appDir, "[agy] "+msg)
		res := fallbackAgy(appDir, nowIso, msg)
		res.Raw = parsed
		return res
	}
	resetAgyAuthLatch() // успех — снимаем latch
	saveAgyCache(parsed, inner)

	return &AgyResult{
		Status: "online", LastUpdated: nowIso,
		Buckets: buckets, Raw: parsed,
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
	// если есть фрагмент/корень с бакетами — берём их (cached valid stale не стираем)
	if frag := loadCachedProviderBlocks(appDir, "agy"); frag != nil {
		b := agyBucketsFromBlock(frag)
		if b == nil {
			// корневой limits.json: бакеты лежат в data.agy.*
			if d := getMap(frag, "data"); d != nil {
				if agyData := getMap(d, "agy"); agyData != nil {
					b = agyBucketsFromBlock(agyData)
				}
			}
		}
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
