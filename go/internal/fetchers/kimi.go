package fetchers

import (
	"encoding/json"

	"llmlimits/internal/config"
	"llmlimits/internal/env"
	"llmlimits/internal/model"
)

// FetchKimi — окна 5ч/7д из {baseUrl}/usages.
func FetchKimi(appDir string, p config.Provider, ep *env.Parser) map[string]interface{} {
	id := p.Id
	key := resolveKey(p, ep)
	nowIso := NowIso()

	if key == "" || env.IsPlaceholderKey(key) {
		logNoKeyOnce(appDir, id)
		return saveFragment(appDir, id, nowIso, defaultKimi("no_key"))
	}

	base := p.BaseUrl
	if base == "" {
		base = "https://api.kimi.com/coding/v1"
	}
	_, _, body, err := HttpGet(base+"/usages", key)
	if err != nil {
		LogLine(appDir, "["+id+"] Request error: "+err.Error())
		return fallbackKimi(appDir, id, nowIso, err.Error())
	}

	var root map[string]interface{}
	if err := json.Unmarshal(body, &root); err != nil {
		return fallbackKimi(appDir, id, nowIso, "invalid JSON: "+err.Error())
	}
	usages := getMap(root, "usages")
	u5h := getMap(usages, "limit_5h")
	u7d := getMap(usages, "limit_7d")

	// 5h
	var ratio5h *float64
	var time5h string
	if u5h != nil && hasNonNull(u5h, "used_ratio") {
		f := getFloat(u5h, "used_ratio", 0)
		ratio5h = &f
		time5h = getStr(u5h, "reset_time")
	}
	if ratio5h == nil {
		for _, limObj := range getList(root, "limits") {
			lim, ok := limObj.(map[string]interface{})
			if !ok {
				continue
			}
			w := getMap(lim, "window")
			if w != nil && getInt(w, "duration", 0) == 300 {
				det := getMap(lim, "detail")
				lVal := getFloat(det, "limit", 100)
				uVal := getFloat(det, "used", 0)
				f := 0.0
				if lVal > 0 {
					f = uVal / lVal
				}
				ratio5h = &f
				time5h = getStr(det, "resetTime")
				break
			}
		}
	}
	if ratio5h == nil {
		z := 0.0
		ratio5h = &z
	}

	// 7d
	var ratio7d *float64
	var time7d string
	if u7d != nil && hasNonNull(u7d, "used_ratio") {
		f := getFloat(u7d, "used_ratio", 0)
		ratio7d = &f
		time7d = getStr(u7d, "reset_time")
	}
	if ratio7d == nil {
		if usageObj := getMap(root, "usage"); usageObj != nil {
			lVal := getFloat(usageObj, "limit", 100)
			uVal := getFloat(usageObj, "used", 0)
			f := 0.0
			if lVal > 0 {
				f = uVal / lVal
			}
			ratio7d = &f
			time7d = getStr(usageObj, "resetTime")
		}
	}
	if ratio7d == nil {
		z := 0.0
		ratio7d = &z
	}

	res := defaultKimi("online")
	res["last_updated"] = nowIso
	res["limit_5h"] = kimiWindow(clamp01(*ratio5h), time5h)
	res["limit_7d"] = kimiWindow(clamp01(*ratio7d), time7d)
	return saveFragment(appDir, id, nowIso, res)
}

func kimiWindow(frac float64, resetTime string) map[string]interface{} {
	pct := round1(frac * 100)
	resetIn := "--"
	if resetTime != "" {
		resetIn = FormatTimeDiff(resetTime, "--")
	}
	return map[string]interface{}{
		"used_ratio":   round4(frac),
		"used_percent": pct,
		"reset_in":     resetIn,
		"reset_time":   resetTime,
		"level":        GetUsedStatusLevel(frac),
	}
}

func defaultKimi(status string) map[string]interface{} {
	return map[string]interface{}{
		"status":       status,
		"last_updated": "",
		"limit_5h":     kimiWindow(0, ""),
		"limit_7d":     kimiWindow(0, ""),
	}
}

func fallbackKimi(appDir, id, nowIso, errMsg string) map[string]interface{} {
	var res map[string]interface{}
	if prev := loadCachedProvider(appDir, id); prev != nil &&
		(getStr(prev, "status") == "online" || getStr(prev, "status") == "stale") {
		res = prev
		res["status"] = "stale"
		res["error"] = errMsg
	} else {
		res = defaultKimi("error")
		res["error"] = errMsg
	}
	return saveFragment(appDir, id, nowIso, res)
}

// KimiToState — map -> model.KimiInfo.
func KimiToState(block map[string]interface{}) *model.KimiInfo {
	k := &model.KimiInfo{
		Status:      getStr(block, "status"),
		LastUpdated: getStr(block, "last_updated"),
	}
	k.Limit5h = bucketFromMap(getMap(block, "limit_5h"))
	k.Limit7d = bucketFromMap(getMap(block, "limit_7d"))
	return k
}

func bucketFromMap(m map[string]interface{}) model.Bucket {
	if m == nil {
		return model.Bucket{ResetIn: "--"}
	}
	// kimi-блоки хранят used_ratio (формат файла совместим с C#),
	// аги-бакеты — used_fraction. Порядок: used_ratio -> used_fraction.
	frac := getFloat(m, "used_ratio", 0)
	if !hasNonNull(m, "used_ratio") {
		frac = getFloat(m, "used_fraction", 0)
	}
	return model.Bucket{
		UsedPercent:  getFloat(m, "used_percent", 0),
		UsedFraction: frac,
		ResetIn:      getStr(m, "reset_in"),
		// Явная заглушка (аги fallback-бакеты): не рисовать как «0%% использовано».
		Placeholder: getBool(m, "placeholder") || getStr(m, "level") == "unknown",
	}
}

func resolveKey(p config.Provider, ep *env.Parser) string {
	if p.ApiKey != "" {
		return p.ApiKey
	}
	if p.ApiKeyEnv != "" && ep != nil {
		return ep.Get(p.ApiKeyEnv)
	}
	return ""
}

var noKeyLogged = map[string]bool{}

func logNoKeyOnce(appDir, id string) {
	if noKeyLogged[id] {
		return
	}
	noKeyLogged[id] = true
	LogLine(appDir, "["+id+"] ключ не задан (placeholder) — HTTP пропущен")
}
