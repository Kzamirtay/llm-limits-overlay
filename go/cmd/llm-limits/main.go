package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"llmlimits/internal/config"
	"llmlimits/internal/fetchers"
	"llmlimits/internal/model"
)

type options struct {
	json        bool
	provider    string
	watchSec    int
	noCache     bool
	envPath     string
	configPath  string
	agyBin      string
	agyTimeout  int
	noColor     bool
	help        bool
	version     bool
}

var colorEnabled bool
var providers []config.Provider

func main() {
	os.Exit(run(os.Args[1:]))
}

func run(args []string) int {
	opt, errMsg := parseArgs(args)
	if errMsg != "" {
		fmt.Fprintln(os.Stderr, "llm-limits: "+errMsg)
		fmt.Fprintln(os.Stderr, "Try 'llm-limits --help' for usage.")
		return 1
	}
	if opt.help {
		printHelp()
		return 0
	}
	if opt.version {
		fmt.Println("llm-limits 2.1.1")
		return 0
	}

	colorEnabled = !opt.noColor && os.Getenv("NO_COLOR") == "" && isTTY()

	exe, err := os.Executable()
	if err != nil {
		exe = os.Args[0]
	}
	appDir := filepath.Dir(exe)
	config.SetLogFunc(func(msg string) {
		fetchers.LogLine(appDir, msg)
	})

	cfgPath := opt.configPath
	if cfgPath == "" {
		cfgPath = filepath.Join(appDir, "overlay.config.json")
	}
	cfg := config.Load(cfgPath)
	providers = cfg.EffectiveProviders()

	if opt.provider != "" {
		found := false
		for _, p := range providers {
			if strings.EqualFold(p.Id, opt.provider) {
				found = true
			}
		}
		if !found {
			if strings.EqualFold(opt.provider, "codex") {
				codexP := config.Provider{Id: "codex", Type: "codex", Title: "CODEX", Enabled: true}
				providers = append(providers, codexP)
				cfg.Providers = append(cfg.Providers, codexP)
				found = true
			} else {
				fmt.Fprintf(os.Stderr, "llm-limits: unknown provider id '%s' (check overlay.config.json providers).\n", opt.provider)
				return 1
			}
		}
	}

	if opt.noCache {
		fetchers.SetAgyBypassCache(true)
	}
	if opt.agyBin != "" {
		os.Setenv("LLM_LIMITS_AGY_BIN", opt.agyBin)
	}

	if opt.watchSec > 0 {
		return runWatch(opt, cfg, appDir)
	}

	snap := fetchOnce(opt, cfg, appDir)
	if snap == nil {
		fmt.Fprintln(os.Stderr, "llm-limits: fetch failed and no cache available.")
		return 1
	}
	render(opt, snap)
	return exitCode(opt, snap)
}

func parseArgs(args []string) (*options, string) {
	opt := &options{}
	for i := 0; i < len(args); i++ {
		a := args[i]
		next := func() (string, bool) {
			if i+1 < len(args) {
				i++
				return args[i], true
			}
			return "", false
		}
		switch a {
		case "--json":
			opt.json = true
		case "--provider":
			v, ok := next()
			if !ok {
				return nil, "--provider requires a provider id."
			}
			opt.provider = strings.ToLower(strings.TrimSpace(v))
		case "--watch":
			v, ok := next()
			if !ok {
				return nil, "--watch requires seconds value."
			}
			n, err := strconv.Atoi(v)
			if err != nil || n < 2 {
				return nil, "--watch value must be an integer >= 2."
			}
			opt.watchSec = n
		case "--no-cache":
			opt.noCache = true
		case "--env":
			v, ok := next()
			if !ok {
				return nil, "--env requires a path."
			}
			opt.envPath = v
		case "--config":
			v, ok := next()
			if !ok {
				return nil, "--config requires a path."
			}
			opt.configPath = v
		case "--agy-bin":
			v, ok := next()
			if !ok {
				return nil, "--agy-bin requires a path."
			}
			opt.agyBin = v
		case "--agy-timeout":
			v, ok := next()
			if !ok {
				return nil, "--agy-timeout requires seconds (1..120)."
			}
			n, err := strconv.Atoi(v)
			if err != nil || n < 1 || n > 120 {
				return nil, "--agy-timeout value must be an integer 1..120."
			}
			opt.agyTimeout = n
		case "--no-color":
			opt.noColor = true
		case "--version", "-v":
			opt.version = true
		case "--help", "-h", "/?":
			opt.help = true
		default:
			return nil, "unknown argument '" + a + "'."
		}
	}
	return opt, ""
}

func defaultAgyTimeout() int {
	if os.Getenv("GOOS") == "windows" || isWindows() {
		return 8
	}
	return 20
}

func isWindows() bool {
	return filepath.Separator == '\\' || os.PathSeparator == '\\'
}

func effectiveAgyTimeout(opt *options) int {
	if opt.agyTimeout > 0 {
		return opt.agyTimeout
	}
	if v := os.Getenv("LLM_LIMITS_AGY_TIMEOUT"); v != "" {
		if n, err := strconv.Atoi(strings.TrimSpace(v)); err == nil && n >= 1 && n <= 120 {
			return n
		}
	}
	return defaultAgyTimeout()
}

func fetchOnce(opt *options, cfg *config.Config, appDir string) *model.Snapshot {
	timeout := effectiveAgyTimeout(opt)
	budgetSec := cfg.TimeoutSec
	budgetMs := 0
	if budgetSec > 0 && int(budgetSec*1000) < (timeout+5)*1000 {
		budgetMs = int(budgetSec * 1000)
	}
	effectiveCfg := cfg
	if opt.provider != "" {
		effectiveCfg = &config.Config{
			Path:               cfg.Path,
			TimeoutSec:         cfg.TimeoutSec,
			ClickThrough:       cfg.ClickThrough,
			ClickThroughHotkey: cfg.ClickThroughHotkey,
		}
		for _, p := range cfg.EffectiveProviders() {
			if strings.EqualFold(p.Id, opt.provider) {
				effectiveCfg.Providers = append(effectiveCfg.Providers, p)
			}
		}
	}
	return fetchers.FetchAll(effectiveCfg, fetchers.Options{
		AppDir:        appDir,
		EnvPath:       opt.envPath,
		AgyTimeoutSec: timeout,
		BudgetMs:      budgetMs,
	})
}

func runWatch(opt *options, cfg *config.Config, appDir string) int {
	lastCode := 1
	first := true

	// Ctrl+C — выход с последним кодом
	sigCh := make(chan os.Signal, 1)
	notifySignal(sigCh)

	for {
		select {
		case <-sigCh:
			return lastCode
		default:
		}

		snap := fetchOnce(opt, cfg, appDir)
		if snap == nil {
			fmt.Fprintf(os.Stderr, "[%s] fetch failed\n", time.Now().Format("15:04:05"))
			lastCode = 1
		} else {
			lastCode = exitCode(opt, snap)
			if opt.json {
				out, _ := json.Marshal(buildPayload(opt, snap))
				fmt.Println(string(out))
			} else {
				if !first && !isTTY() {
					fmt.Println()
				}
				render(opt, snap)
				if !isTTY() {
					fmt.Println("--- tick " + time.Now().Format("2006-01-02 15:04:05") + " ---")
				}
			}
		}
		first = false

		// сон с возможностью прерывания
		select {
		case <-sigCh:
			return lastCode
		case <-time.After(time.Duration(opt.watchSec) * time.Second):
		}
	}
}

// ------------------------------------------------------------------
// Rendering
// ------------------------------------------------------------------

func render(opt *options, snap *model.Snapshot) {
	if opt.json {
		out, _ := json.MarshalIndent(buildPayload(opt, snap), "", "  ")
		fmt.Println(string(out))
		return
	}
	for _, p := range providers {
		if !p.Enabled {
			continue
		}
		if opt.provider != "" && !strings.EqualFold(opt.provider, p.Id) {
			continue
		}
		st := snap.Find(p.Id)
		if st == nil {
			continue
		}
		switch p.Type {
		case "agy":
			renderHeader(p.Title, st.Status, st.LastUpdated)
			renderBucket("  gemini 7d", snap.GeminiWeekly)
			renderBucket("  gemini 5h", snap.Gemini5h)
			renderBucket("  claude 7d", snap.ClaudeWeekly)
			renderBucket("  claude 5h", snap.Claude5h)
		case "openrouter":
			if st.OpenRouter == nil {
				continue
			}
			or := st.OpenRouter
			renderHeader(p.Title, st.Status, st.LastUpdated)
			pct := fmt.Sprintf("%s%% used", trimFloat(or.UsedPercent))
			if or.Limit <= 0 {
				pct += " (no limit)"
			}
			fmt.Printf("       %s  reset %s  free %d/%d  rem. $%s\n",
				pct, safe(or.ResetIn), or.FreeDailyUsed, or.FreeDailyLimit,
				fmt.Sprintf("%.2f", or.LimitRemaining))
		case "codex":
			if st.Codex == nil {
				continue
			}
			renderHeader(p.Title, st.Status, st.LastUpdated)
			label := st.Codex.PrimaryName
			if label == "" {
				label = "30д"
			}
			renderBucket("  "+label, st.Codex.Primary)
			if st.Codex.Secondary != nil {
				secLabel := st.Codex.SecondaryName
				if secLabel == "" {
					secLabel = "secondary"
				}
				renderBucket("  "+secLabel, *st.Codex.Secondary)
			}
		default: // kimi
			if st.Kimi == nil {
				continue
			}
			renderHeader(p.Title, st.Status, st.LastUpdated)
			renderBucket("  7d", st.Kimi.Limit7d)
			renderBucket("  5h", st.Kimi.Limit5h)
		}
	}
}

func renderHeader(name, status, lastUpdated string) {
	color := statusColor(status)
	writeColored(name+"  ", color)
	writeColored(status, color)
	age := relativeAge(lastUpdated)
	if age != "" {
		fmt.Println("  (" + age + ")")
	} else {
		fmt.Println()
	}
}

func renderBucket(label string, b model.Bucket) {
	fmt.Printf("       %s  %s%%  reset %s\n", label, trimFloat(b.UsedPercent), safe(b.ResetIn))
}

func safe(s string) string {
	if s == "" {
		return "--"
	}
	return s
}

func trimFloat(f float64) string {
	s := fmt.Sprintf("%.1f", f)
	s = strings.TrimSuffix(s, ".0")
	return s
}

func statusColor(status string) string {
	if !colorEnabled {
		return ""
	}
	switch strings.ToLower(status) {
	case "online":
		return "\x1b[32m"
	case "stale":
		return "\x1b[33m"
	case "no_key":
		return "\x1b[37m"
	case "error":
		return "\x1b[31m"
	}
	return ""
}

const colorReset = "\x1b[0m"

func writeColored(text, color string) {
	if color != "" {
		fmt.Print(color + text + colorReset)
	} else {
		fmt.Print(text)
	}
}

// relativeAge — «только что» / «N мин» / «Nч Mм» (парсинг локального ISO).
func relativeAge(updatedStr string) string {
	if updatedStr == "" {
		return ""
	}
	dt, err := time.ParseInLocation("2006-01-02 15:04:05", updatedStr, time.Local)
	if err != nil {
		return ""
	}
	totalMinutes := int(time.Since(dt).Minutes())
	if totalMinutes < 1 {
		return "только что"
	}
	if totalMinutes < 60 {
		return fmt.Sprintf("%d мин", totalMinutes)
	}
	h := totalMinutes / 60
	m := totalMinutes % 60
	if m > 0 {
		return fmt.Sprintf("%dч %dм", h, m)
	}
	return fmt.Sprintf("%dч", h)
}

// ------------------------------------------------------------------
// JSON payload (та же схема, что у C#-CLI)
// ------------------------------------------------------------------

func buildPayload(opt *options, snap *model.Snapshot) map[string]interface{} {
	pr := map[string]interface{}{}
	for _, p := range providers {
		if !p.Enabled {
			continue
		}
		if opt.provider != "" && !strings.EqualFold(opt.provider, p.Id) {
			continue
		}
		st := snap.Find(p.Id)
		if st == nil {
			continue
		}
		switch p.Type {
		case "agy":
			pr[p.Id] = map[string]interface{}{
				"status":         st.Status,
				"last_updated":   st.LastUpdated,
				"gemini_5h":      bucketJson(snap.Gemini5h),
				"gemini_weekly":  bucketJson(snap.GeminiWeekly),
				"claude_5h":      bucketJson(snap.Claude5h),
				"claude_weekly":  bucketJson(snap.ClaudeWeekly),
			}
		case "openrouter":
			if st.OpenRouter == nil {
				continue
			}
			or := st.OpenRouter
			pr[p.Id] = map[string]interface{}{
				"status":           st.Status,
				"last_updated":     st.LastUpdated,
				"limit":            or.Limit,
				"usage":            or.Usage,
				"limit_remaining":  or.LimitRemaining,
				"used_percent":     or.UsedPercent,
				"used_fraction":    or.UsedFraction,
				"reset_in":         or.ResetIn,
				"free_daily_used":  or.FreeDailyUsed,
				"free_daily_limit": or.FreeDailyLimit,
			}
		case "codex":
			if st.Codex == nil {
				continue
			}
			cx := st.Codex
			m := map[string]interface{}{
				"status":       st.Status,
				"last_updated": st.LastUpdated,
				"plan_type":    cx.PlanType,
				"balance":      cx.Balance,
				"primary":      bucketJson(cx.Primary),
			}
			if cx.PrimaryName != "" {
				m["primary_name"] = cx.PrimaryName
			}
			if cx.Secondary != nil {
				m["secondary"] = bucketJson(*cx.Secondary)
				if cx.SecondaryName != "" {
					m["secondary_name"] = cx.SecondaryName
				}
			}
			pr[p.Id] = m
		default:
			if st.Kimi == nil {
				continue
			}
			pr[p.Id] = map[string]interface{}{
				"status":       st.Status,
				"last_updated": st.LastUpdated,
				"limit_5h":     bucketJson(st.Kimi.Limit5h),
				"limit_7d":     bucketJson(st.Kimi.Limit7d),
			}
		}
	}
	return map[string]interface{}{
		"status":       snap.Status,
		"last_updated": snap.LastUpdated,
		"providers":    pr,
	}
}

func bucketJson(b model.Bucket) map[string]interface{} {
	return map[string]interface{}{
		"used_percent":  b.UsedPercent,
		"used_fraction": b.UsedFraction,
		"reset_in":      b.ResetIn,
	}
}

// ------------------------------------------------------------------
// Exit codes
// ------------------------------------------------------------------

func exitCode(opt *options, snap *model.Snapshot) int {
	if opt.provider != "" {
		st := snap.Find(opt.provider)
		if st == nil {
			return 1
		}
		return codeFor(strings.ToLower(st.Status))
	}
	if strings.ToLower(snap.Status) == "error" {
		return 1
	}
	online, total := 0, 0
	for _, p := range providers {
		if !p.Enabled {
			continue
		}
		st := snap.Find(p.Id)
		if st == nil {
			continue
		}
		total++
		if strings.ToLower(st.Status) == "online" {
			online++
		}
	}
	if total == 0 {
		return 1
	}
	if online == total {
		return 0
	}
	if online == 0 {
		return 1
	}
	return 2
}

func codeFor(status string) int {
	switch status {
	case "online":
		return 0
	case "stale", "no_key", "disabled":
		return 2
	default:
		return 1
	}
}

func isTTY() bool {
	fi, err := os.Stdout.Stat()
	if err != nil {
		return false
	}
	return fi.Mode()&os.ModeCharDevice != 0
}

func printHelp() {
	fmt.Println("llm-limits 2.1.1 - LLM quota status in the terminal (Kimi / OpenRouter / Gemini+Claude-GPT / Codex)")
	fmt.Println()
	fmt.Println("Usage: llm-limits [options]")
	fmt.Println()
	fmt.Println("Options:")
	fmt.Println("  (no args)               Human-readable table of all providers")
	fmt.Println("  --json                  Machine-readable JSON output")
	fmt.Println("  --provider <id>         Only the provider with this id (from overlay.config.json)")
	fmt.Println("  --watch <seconds>       Refresh loop (>= 2s); Ctrl+C to exit")
	fmt.Println("  --no-cache              Bypass the agy /usage cache (forces agy run)")
	fmt.Println("  --env <path>            Use explicit .env file")
	fmt.Println("  --config <path>         Use explicit overlay.config.json")
	fmt.Println("  --agy-bin <path>        Explicit path to agy binary")
	fmt.Println("  --agy-timeout <sec>     agy spawn timeout, 1..120 (default: Windows 8s, Linux/macOS 20s; env LLM_LIMITS_AGY_TIMEOUT)")
	fmt.Println("  --no-color              Disable ANSI colors (also honors NO_COLOR)")
	fmt.Println("  --version, -v           Show version (2.1.1)")
	fmt.Println("  --help                  Show this help")
	fmt.Println()
	fmt.Println("Exit codes: 0 = all providers online, 2 = some stale, 1 = all failed / config error")
	fmt.Println("API keys and OpenRouter labels are never printed.")
}
