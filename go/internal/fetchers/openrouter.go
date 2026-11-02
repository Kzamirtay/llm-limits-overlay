package fetchers

import (
	"encoding/json"
	"math"

	"llmlimits/internal/config"
	"llmlimits/internal/env"
	"llmlimits/internal/model"
)

// FetchOpenRouter — кредиты + free-пул из {baseUrl}/key.
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

	res := parseOpenRouterData(data, nowIso)
	return saveFragment(appDir, id, nowIso, res)
}

func parseOpenRouterData(data map[string]interface{}, nowIso string) map[string]interface{} {
	usageAllTime := getFloat(data, "usage", 0)

	var limitNum float64
	var hasLimit bool
	var spendVal float64
	var remVal float64
	var limitReset string
	var resetsAt string

	eb := getMap(data, "effective_budget")
	if isValidEffectiveBudget(eb) {
		hasLimit = hasNonNull(eb, "limit_usd")
		if hasLimit {
			limitNum = getFloat(eb, "limit_usd", 0)
		}

		hasSpend := hasNonNull(eb, "spend_usd")
		hasRemaining := hasNonNull(eb, "remaining_usd")

		if hasSpend {
			spendVal = getFloat(eb, "spend_usd", 0)
		}
		if hasRemaining {
			remVal = getFloat(eb, "remaining_usd", 0)
		}

		if !hasSpend && hasLimit && hasRemaining {
			spendVal = limitNum - remVal
			if spendVal < 0 {
				spendVal = 0
			}
		} else if !hasRemaining && hasLimit && hasSpend {
			remVal = limitNum - spendVal
			if remVal < 0 {
				remVal = 0
			}
		}

		// null / missing reset_interval in effective_budget means no periodic reset
		if hasNonNull(eb, "reset_interval") {
			limitReset = getStr(eb, "reset_interval")
		} else {
			limitReset = ""
		}

		resetsAt = getStr(eb, "resets_at")
		if resetsAt == "" && limitReset != "" && limitReset != "none" {
			budgets := getList(data, "budgets")
			resetsAt = findEffectiveBudgetReset(eb, budgets)
		}
	} else {
		hasLimit = hasNonNull(data, "limit")
		if hasLimit {
			limitNum = getFloat(data, "limit", 0)
		}

		// null / missing limit_reset in data means no periodic reset (lifetime)
		if hasNonNull(data, "limit_reset") {
			limitReset = getStr(data, "limit_reset")
		} else {
			limitReset = ""
		}

		includeByok := getBool(data, "include_byok_in_limit") || getBool(data, "include_byok")
		switch limitReset {
		case "daily":
			if hasNonNull(data, "usage_daily") {
				spendVal = getFloat(data, "usage_daily", 0)
				if includeByok {
					spendVal += getFloat(data, "byok_usage_daily", 0)
				}
			} else if hasLimit && hasNonNull(data, "limit_remaining") {
				spendVal = limitNum - getFloat(data, "limit_remaining", 0)
			} else {
				spendVal = getFloat(data, "usage_daily", 0)
			}
		case "weekly":
			if hasNonNull(data, "usage_weekly") {
				spendVal = getFloat(data, "usage_weekly", 0)
				if includeByok {
					spendVal += getFloat(data, "byok_usage_weekly", 0)
				}
			} else if hasLimit && hasNonNull(data, "limit_remaining") {
				spendVal = limitNum - getFloat(data, "limit_remaining", 0)
			} else {
				spendVal = getFloat(data, "usage_weekly", 0)
			}
		case "monthly":
			if hasNonNull(data, "usage_monthly") {
				spendVal = getFloat(data, "usage_monthly", 0)
				if includeByok {
					spendVal += getFloat(data, "byok_usage_monthly", 0)
				}
			} else if hasLimit && hasNonNull(data, "limit_remaining") {
				spendVal = limitNum - getFloat(data, "limit_remaining", 0)
			} else {
				spendVal = getFloat(data, "usage_monthly", 0)
			}
		default:
			spendVal = usageAllTime
			if includeByok {
				spendVal += getFloat(data, "byok_usage", 0)
			}
		}
		if spendVal < 0 {
			spendVal = 0
		}

		if hasNonNull(data, "limit_remaining") {
			remVal = getFloat(data, "limit_remaining", 0)
		} else if hasLimit {
			remVal = limitNum - spendVal
			if remVal < 0 {
				remVal = 0
			}
		}

		if limitReset != "" && limitReset != "none" {
			budgets := getList(data, "budgets")
			resetsAt = findLegacyKeyBudgetReset(budgets, limitReset)
		}
	}

	if limitReset == "" || limitReset == "none" {
		resetsAt = ""
	}

	resetIn := "--"
	if resetsAt != "" {
		resetIn = FormatTimeDiff(resetsAt, "--")
	}

	usedFrac, usedPct := 0.0, 0.0
	if !hasLimit {
		usedFrac = 0.0
		usedPct = 0.0
	} else if limitNum <= 0 {
		if spendVal > 0 {
			usedFrac = 1.0
			usedPct = 100.0
		} else {
			usedFrac = 0.0
			usedPct = 0.0
		}
	} else {
		usedFrac = clamp01(spendVal / limitNum)
		usedPct = round1(usedFrac * 100)
	}

	freeDaily := getMap(data, "free_model_daily_requests")
	freeUsed := getInt(freeDaily, "used", 0)
	freeLimit := getInt(freeDaily, "limit", 1000)

	res := defaultOpenRouter("online")
	res["last_updated"] = nowIso
	res["limit"] = limitNum
	res["usage"] = round4(usageAllTime)
	res["usage_total"] = round4(usageAllTime)
	res["usage_period"] = round4(spendVal)
	res["limit_remaining"] = round2(remVal)
	res["used_fraction"] = round4(usedFrac)
	res["used_percent"] = usedPct
	res["limit_reset"] = limitReset
	res["resets_at"] = resetsAt
	res["reset_in"] = resetIn
	res["level"] = GetUsedStatusLevel(usedFrac)
	res["free_daily_used"] = freeUsed
	freeLimitVal := freeLimit
	res["free_daily_limit"] = freeLimitVal
	res["label"] = MaskOpenRouterLabel(getStr(data, "label"))
	return res
}

func isValidEffectiveBudget(eb map[string]interface{}) bool {
	if eb == nil {
		return false
	}
	hasLimit := hasNonNull(eb, "limit_usd")
	hasSpend := hasNonNull(eb, "spend_usd")
	hasRemaining := hasNonNull(eb, "remaining_usd")

	if hasLimit && (hasSpend || hasRemaining) {
		return true
	}
	if hasSpend && hasRemaining {
		return true
	}
	if val, exists := eb["limit_usd"]; exists && val == nil && hasSpend {
		return true
	}
	return false
}

func budgetMatches(eb, b map[string]interface{}) bool {
	if eb == nil || b == nil {
		return false
	}
	ebScope := getStr(eb, "scope")
	bScope := getStr(b, "scope")
	if ebScope != bScope {
		return false
	}

	ebID := getStr(eb, "budget_id")
	if ebID == "" {
		ebID = getStr(eb, "guardrail_id")
	}
	if ebID == "" {
		ebID = getStr(eb, "id")
	}

	bID := getStr(b, "budget_id")
	if bID == "" {
		bID = getStr(b, "guardrail_id")
	}
	if bID == "" {
		bID = getStr(b, "id")
	}

	if ebID != "" || bID != "" {
		if ebID != bID {
			return false
		}
	}

	ebInterval := getStr(eb, "reset_interval")
	bInterval := getStr(b, "reset_interval")
	if ebInterval != "" && bInterval != "" && ebInterval != bInterval {
		return false
	}

	if hasNonNull(eb, "limit_usd") && hasNonNull(b, "limit_usd") {
		if getFloat(eb, "limit_usd", 0) != getFloat(b, "limit_usd", 0) {
			return false
		}
	}

	return true
}

func findEffectiveBudgetReset(eb map[string]interface{}, budgets []interface{}) string {
	if eb == nil {
		return ""
	}
	ebInterval := getStr(eb, "reset_interval")
	if ebInterval == "" || ebInterval == "none" {
		return ""
	}

	var match string
	matchCount := 0
	for _, item := range budgets {
		bm, ok := item.(map[string]interface{})
		if !ok {
			continue
		}
		if budgetMatches(eb, bm) {
			match = getStr(bm, "resets_at")
			matchCount++
		}
	}
	if matchCount == 1 {
		return match
	}
	return ""
}

func findLegacyKeyBudgetReset(budgets []interface{}, limitReset string) string {
	if limitReset == "" || limitReset == "none" {
		return ""
	}
	var match string
	matchCount := 0
	for _, item := range budgets {
		bm, ok := item.(map[string]interface{})
		if !ok {
			continue
		}
		scope := getStr(bm, "scope")
		if scope != "key_limit" {
			continue
		}
		interval := getStr(bm, "reset_interval")
		if interval != limitReset {
			continue
		}
		match = getStr(bm, "resets_at")
		matchCount++
	}
	if matchCount == 1 {
		return match
	}
	return ""
}

func round2(f float64) float64 {
	return math.Round(f*100) / 100
}

func defaultOpenRouter(status string) map[string]interface{} {
	return map[string]interface{}{
		"status":           status,
		"last_updated":     "",
		"limit":            0.0,
		"usage":            0.0,
		"usage_total":      0.0,
		"usage_period":     0.0,
		"limit_remaining":  0.0,
		"used_percent":     0.0,
		"used_fraction":    0.0,
		"reset_in":         "--",
		"limit_reset":      "",
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
		UsagePeriod:    getFloat(block, "usage_period", 0),
		UsageTotal:     getFloat(block, "usage_total", 0),
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
