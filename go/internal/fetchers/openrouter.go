package fetchers

import (
	"encoding/json"

	"llmlimits/internal/config"
	"llmlimits/internal/env"
	"llmlimits/internal/model"
)

// FetchOpenRouter — кредиты + free-пул из {baseUrl}/key (1:1 с C# FetchWorkerOpenRouter).
func FetchOpenRouter(appDir string, p config.Provider, ep *env.Parser) map[string]interface{} {
	id := p.Id
	key := resolveKey(p, ep)
	nowIso := NowIso()

	if key == "" || env.IsPlaceholderKey(key) {
		logNoKeyOnce(appDir, id)
		return saveFragment(appDir, id, nowIso, defaultOpenRouter("no_key"))
	}

	base := p.BaseUrl
	if base == "" {
		base = "https://openrouter.ai/api/v1"
	}
	_, _, body, err := HttpGet(base+"/key", key)
	if err != nil {
		LogLine(appDir, "["+id+"] Request error: "+err.Error())
		return fallbackOpenRouter(appDir, id, nowIso, err.Error())
	}

	var root map[string]interface{}
	if err := json.Unmarshal(body, &root); err != nil {
		return fallbackOpenRouter(appDir, id, nowIso, "invalid JSON: "+err.Error())
	}
	data := getMap(root, "data")
	if data == nil {
		data = map[string]interface{}{}
	}

	var limitNum float64
	if hasNonNull(data, "limit") {
		limitNum = getFloat(data, "limit", 0)
	}
	usage := getFloat(data, "usage", 0)
	limitRemaining := getFloat(data, "limit_remaining", 0)
	limitReset := getStr(data, "limit_reset")
	if limitReset == "" {
		limitReset = "monthly"
	}

	resetsAt := ""
	if budgets := getList(data, "budgets"); len(budgets) > 0 {
		if b0, ok := budgets[0].(map[string]interface{}); ok {
			resetsAt = getStr(b0, "resets_at")
		}
	}
	if resetsAt == "" {
		if eb := getMap(data, "effective_budget"); eb != nil {
			resetsAt = getStr(eb, "resets_at")
		}
	}
	resetIn := "--"
	if resetsAt != "" {
		resetIn = FormatTimeDiff(resetsAt, "--")
	}

	usedFrac, usedPct := 0.0, 0.0
	if limitNum > 0 {
		usedFrac = clamp01(usage / limitNum)
		usedPct = round1(usedFrac * 100)
	}

	freeDaily := getMap(data, "free_model_daily_requests")
	freeUsed := getInt(freeDaily, "used", 0)
	freeLimit := getInt(freeDaily, "limit", 1000)

	res := defaultOpenRouter("online")
	res["last_updated"] = nowIso
	res["limit"] = limitNum
	res["usage"] = round4(usage)
	res["limit_remaining"] = round2(limitRemaining)
	res["used_fraction"] = round4(usedFrac)
	res["used_percent"] = usedPct
	res["limit_reset"] = limitReset
	res["resets_at"] = resetsAt
	res["reset_in"] = resetIn
	res["level"] = GetUsedStatusLevel(usedFrac)
	res["free_daily_used"] = freeUsed
	res["free_daily_limit"] = freeLimit
	res["label"] = MaskOpenRouterLabel(getStr(data, "label"))
	return saveFragment(appDir, id, nowIso, res)
}

func round2(f float64) float64 {
	return float64(int(f*100+0.5)) / 100
}

func defaultOpenRouter(status string) map[string]interface{} {
	return map[string]interface{}{
		"status":           status,
		"last_updated":     "",
		"limit":            0.0,
		"usage":            0.0,
		"limit_remaining":  0.0,
		"used_percent":     0.0,
		"used_fraction":    0.0,
		"reset_in":         "--",
		"limit_reset":      "monthly",
		"resets_at":        "",
		"level":            "unknown",
		"free_daily_used":  0,
		"free_daily_limit": 1000,
		"label":            "",
	}
}

func fallbackOpenRouter(appDir, id, nowIso, errMsg string) map[string]interface{} {
	var res map[string]interface{}
	if prev := loadCachedProvider(appDir, id); prev != nil &&
		(getStr(prev, "status") == "online" || getStr(prev, "status") == "stale") {
		res = prev
		res["status"] = "stale"
		res["error"] = errMsg
	} else {
		res = defaultOpenRouter("error")
		res["error"] = errMsg
	}
	return saveFragment(appDir, id, nowIso, res)
}

// OpenRouterToState — map -> model.OpenRouterInfo.
func OpenRouterToState(block map[string]interface{}) *model.OpenRouterInfo {
	or := &model.OpenRouterInfo{
		Status:         getStr(block, "status"),
		LastUpdated:    getStr(block, "last_updated"),
		Limit:          getFloat(block, "limit", 0),
		Usage:          getFloat(block, "usage", 0),
		LimitRemaining: getFloat(block, "limit_remaining", 0),
		UsedPercent:    getFloat(block, "used_percent", 0),
		UsedFraction:   getFloat(block, "used_fraction", 0),
		ResetIn:        getStr(block, "reset_in"),
		LimitReset:     getStr(block, "limit_reset"),
		FreeDailyUsed:  getInt(block, "free_daily_used", 0),
		FreeDailyLimit: getInt(block, "free_daily_limit", 0),
	}
	return or
}
