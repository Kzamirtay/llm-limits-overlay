package fetchers

import (
	"encoding/json"
	"strings"

	"llmlimits/internal/config"
	"llmlimits/internal/env"
	"llmlimits/internal/model"
)

// OpenCode Go (https://opencode.ai/docs/go): подписка с долларовыми окнами
// $12/5ч, $30/нед, $60/мес. Сервер отдаёт проценты расхода без денежных сумм,
// поэтому деньги в блоке — производные от опубликованных лимитов плана.
const (
	openCodeDefaultBase  = "https://opencode.ai/zen/go/v1"
	openCodeLimitRolling = 12.0
	openCodeLimitWeekly  = 30.0
	openCodeLimitMonthly = 60.0
)

type openCodeWindowSpec struct {
	Key   string
	Label string
	Limit float64
}

var openCodeWindows = []openCodeWindowSpec{
	{"rolling", "5ч", openCodeLimitRolling},
	{"weekly", "7д", openCodeLimitWeekly},
	{"monthly", "30д", openCodeLimitMonthly},
}

// FetchOpenCode — окна 5ч/нед/мес из {baseUrl}/usage (Bearer-ключ консоли Go).
// Формат ответа: {"usage":{"rolling":{"status","percent","resetsAt"},...}}.
func FetchOpenCode(appDir string, p config.Provider, ep *env.Parser) map[string]interface{} {
	id := p.Id
	key := resolveKey(p, ep)
	nowIso := NowIso()

	if key == "" || env.IsPlaceholderKey(key) {
		logNoKeyOnce(appDir, id)
		return saveFragment(appDir, id, nowIso, defaultOpenCode("no_key"))
	}

	base := strings.TrimRight(p.BaseUrl, "/")
	if base == "" {
		base = openCodeDefaultBase
	}
	_, _, body, err := HttpGet(base+"/usage", key)
	if err != nil {
		LogLine(appDir, "["+id+"] Request error: "+err.Error())
		return fallbackOpenCode(appDir, id, nowIso, err.Error())
	}

	var root map[string]interface{}
	if err := json.Unmarshal(body, &root); err != nil {
		return fallbackOpenCode(appDir, id, nowIso, "invalid JSON: "+err.Error())
	}
	usage := getMap(root, "usage")
	if usage == nil {
		return fallbackOpenCode(appDir, id, nowIso, "no usage object in response")
	}

	res := defaultOpenCode("online")
	res["last_updated"] = nowIso
	for _, w := range openCodeWindows {
		res[w.Key] = openCodeWindow(getMap(usage, w.Key), w.Limit)
	}
	return saveFragment(appDir, id, nowIso, res)
}

// openCodeWindow — одно окно; отсутствующее/битое окно остаётся заглушкой,
// чтобы UI не показывал выдуманный «0% использовано».
func openCodeWindow(w map[string]interface{}, limit float64) map[string]interface{} {
	if w == nil || !hasNonNull(w, "percent") {
		return placeholderWindow()
	}
	pct := clampPct(getFloat(w, "percent", 0))
	frac := pct / 100.0
	limited := strings.EqualFold(getStr(w, "status"), "rate-limited")

	resetTime := getStr(w, "resetsAt")
	resetIn := "--"
	if resetTime != "" {
		resetIn = FormatTimeDiff(resetTime, "--")
	}
	level := GetUsedStatusLevel(frac)
	if limited {
		level = "critical"
	}
	return map[string]interface{}{
		"used_ratio":    round4(frac),
		"used_percent":  round1(pct),
		"reset_in":      resetIn,
		"reset_time":    resetTime,
		"level":         level,
		"limited":       limited,
		"limit_usd":     round2(limit),
		"remaining_usd": round2(limit * (1 - frac)),
		"placeholder":   false,
	}
}

func placeholderWindow() map[string]interface{} {
	return map[string]interface{}{
		"used_ratio":    0.0,
		"used_percent":  0.0,
		"reset_in":      "--",
		"reset_time":    "",
		"level":         "unknown",
		"limited":       false,
		"limit_usd":     0.0,
		"remaining_usd": 0.0,
		"placeholder":   true,
	}
}

func defaultOpenCode(status string) map[string]interface{} {
	res := map[string]interface{}{
		"status":       status,
		"last_updated": "",
		"plan":         "go",
	}
	for _, w := range openCodeWindows {
		res[w.Key] = placeholderWindow()
	}
	return res
}

func fallbackOpenCode(appDir, id, nowIso, errMsg string) map[string]interface{} {
	var res map[string]interface{}
	if prev := loadCachedProvider(appDir, id); prev != nil &&
		(getStr(prev, "status") == "online" || getStr(prev, "status") == "stale") &&
		getMap(prev, "rolling") != nil {
		res = prev
		res["status"] = "stale"
		res["error"] = errMsg
	} else {
		res = defaultOpenCode("error")
		res["error"] = errMsg
	}
	return saveFragment(appDir, id, nowIso, res)
}

func clampPct(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 100 {
		return 100
	}
	return v
}

// OpenCodeToState — map -> model.OpenCodeInfo.
func OpenCodeToState(block map[string]interface{}) *model.OpenCodeInfo {
	if block == nil {
		return nil
	}
	oc := &model.OpenCodeInfo{
		Status:      getStr(block, "status"),
		LastUpdated: getStr(block, "last_updated"),
		Plan:        getStr(block, "plan"),
	}
	oc.Rolling = windowFromMap("5ч", getMap(block, "rolling"))
	oc.Weekly = windowFromMap("7д", getMap(block, "weekly"))
	oc.Monthly = windowFromMap("30д", getMap(block, "monthly"))
	return oc
}

func windowFromMap(name string, m map[string]interface{}) model.Window {
	if m == nil {
		return model.Window{Name: name, Bucket: model.Bucket{ResetIn: "--", Placeholder: true}}
	}
	return model.Window{
		Name:         name,
		Bucket:       bucketFromMap(m),
		LimitUSD:     getFloat(m, "limit_usd", 0),
		RemainingUSD: getFloat(m, "remaining_usd", 0),
		Limited:      getBool(m, "limited"),
	}
}
