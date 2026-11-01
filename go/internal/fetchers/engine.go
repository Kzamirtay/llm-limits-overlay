package fetchers

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"

	"llmlimits/internal/config"
	"llmlimits/internal/env"
	"llmlimits/internal/model"
)

// Options — параметры одного цикла фетча.
type Options struct {
	AppDir         string // директория бинарника (limits.json/фрагменты/overlay.log)
	EnvPath        string // override .env
	AgyTimeoutSec  int
	BudgetMs       int // общий бюджет цикла
}

// FetchAll — параллельный фетч всех enabled-провайдеров с бюджетом;
// пишет limits.json и фрагменты; возвращает снимок.
func FetchAll(cfg *config.Config, opts Options) *model.Snapshot {
	appDir := opts.AppDir
	envPath := opts.EnvPath
	if envPath == "" {
		envPath = filepath.Join(appDir, ".env")
	}
	ep := env.NewParser(envPath)
	nowIso := NowIso()
	LogLine(appDir, "Starting async native refresh at "+nowIso+"...")

	providers := cfg.EffectiveProviders()

	type providerResult struct {
		id    string
		block map[string]interface{}
	}
	resCh := make(chan providerResult, len(providers))
	var agyCh chan *AgyResult

	for _, p := range providers {
		if !p.Enabled {
			continue
		}
		if p.Type == "agy" {
			if agyCh == nil {
				agyCh = make(chan *AgyResult, 1)
				go func() {
					agyCh <- FetchAgy(appDir, opts.AgyTimeoutSec)
				}()
			}
			continue
		}
		pc := p
		go func() {
			var block map[string]interface{}
			if pc.Type == "openrouter" {
				block = FetchOpenRouter(appDir, pc, ep)
			} else if pc.Type == "codex" {
				block = FetchCodex(appDir, pc)
			} else {
				block = FetchKimi(appDir, pc, ep)
			}
			resCh <- providerResult{id: pc.Id, block: block}
		}()
	}

	// собираем результаты с бюджетом
	budget := opts.BudgetMs
	if budget <= 0 {
		budget = (opts.AgyTimeoutSec + 5) * 1000
	}
	timer := timeAfterMs(budget)

	providerBlocks := map[string]map[string]interface{}{}
	expected := 0
	for _, p := range providers {
		if p.Enabled && p.Type != "agy" {
			expected++
		}
	}
	for i := 0; i < expected; i++ {
		select {
		case r := <-resCh:
			providerBlocks[r.id] = r.block
		case <-timer:
			LogLine(appDir, "fetch budget exceeded; remaining providers falling back to cache")
			// дочитываем что успело; остальные — fallback
			remaining := expected - len(providerBlocks)
			for j := 0; j < remaining; j++ {
				select {
				case r := <-resCh:
					providerBlocks[r.id] = r.block
				default:
				}
			}
			i = expected
		}
	}

	var agyRes *AgyResult
	if agyCh != nil {
		select {
		case agyRes = <-agyCh:
		case <-timer:
			LogLine(appDir, "Agy fetch incomplete or timed out; falling back to cache.")
			agyRes = fallbackAgy(appDir, nowIso, "timeout")
		}
	}

	snap := assemble(cfg, appDir, nowIso, agyRes, providerBlocks)
	return snap
}

func timeAfterMs(ms int) <-chan struct{} {
	ch := make(chan struct{}, 1)
	go func() {
		time.Sleep(time.Duration(ms) * time.Millisecond)
		ch <- struct{}{}
	}()
	return ch
}

// assemble — мердж, запись limits.json/фрагментов, снимок.
func assemble(cfg *config.Config, appDir, nowIso string, agyRes *AgyResult, providerBlocks map[string]map[string]interface{}) *model.Snapshot {
	snap := &model.Snapshot{LastUpdated: nowIso}
	data := map[string]interface{}{}

	allStatuses := []string{}
	if agyRes != nil {
		allStatuses = append(allStatuses, agyRes.Status)
		agyInfo := map[string]interface{}{
			"status":         agyRes.Status,
			"last_updated":   agyRes.LastUpdated,
			"gemini_5h":      agyRes.Buckets.Gemini5h,
			"gemini_weekly":  agyRes.Buckets.GeminiWeekly,
			"claude_5h":      agyRes.Buckets.Claude5h,
			"claude_weekly":  agyRes.Buckets.ClaudeWeekly,
		}
		data["agy"] = agyInfo
		snap.AgyStatus = agyRes.Status
		snap.AgyLastUpdated = agyRes.LastUpdated
		snap.Gemini5h = bucketFromMap(agyRes.Buckets.Gemini5h)
		snap.GeminiWeekly = bucketFromMap(agyRes.Buckets.GeminiWeekly)
		snap.Claude5h = bucketFromMap(agyRes.Buckets.Claude5h)
		snap.ClaudeWeekly = bucketFromMap(agyRes.Buckets.ClaudeWeekly)

		title := "GEMINI + CLAUDE/GPT"
		if cfg != nil {
			if p := cfg.FindProvider("agy"); p != nil && p.Title != "" {
				title = p.Title
			}
		}
		snap.Providers = append(snap.Providers, &model.ProviderState{
			Id: "agy", Kind: "agy", Title: title, Status: agyRes.Status, LastUpdated: agyRes.LastUpdated,
		})
	}

	for id, block := range providerBlocks {
		data[id] = block
		allStatuses = append(allStatuses, getStr(block, "status"))
	}

	rootStatus := "error"
	for _, s := range allStatuses {
		if s == "online" {
			rootStatus = "online"
			break
		}
	}
	snap.Status = rootStatus

	payload := map[string]interface{}{
		"status":       rootStatus,
		"last_updated": nowIso,
		"ls_address":   nil,
		"data":         data,
	}
	if agyRes != nil {
		payload["raw"] = agyRes.Raw
		if agyRes.CacheHit {
			payload["cache_hit"] = true
		}
	}
	if or, ok := providerBlocks["openrouter"]; ok {
		payload["openrouter"] = or
	}
	if km, ok := providerBlocks["kimi"]; ok {
		payload["kimi"] = km
	}
	if cx, ok := providerBlocks["codex"]; ok {
		payload["codex"] = cx
	}

	WriteAtomicJson(filepath.Join(appDir, "limits.json"), payload)

	// ProviderState для CLI/GUI-рендеринга
	for _, id := range orderedIds(providerBlocks) {
		block := providerBlocks[id]
		title := strings.ToUpper(id)
		if cfg != nil {
			if p := cfg.FindProvider(id); p != nil && p.Title != "" {
				title = p.Title
			}
		}
		st := &model.ProviderState{
			Id:          id,
			Title:       title,
			Status:      getStr(block, "status"),
			LastUpdated: getStr(block, "last_updated"),
		}
		if _, ok := block["limit_5h"]; ok {
			st.Kind = "kimi"
			st.Kimi = KimiToState(block)
		} else if _, ok := block["plan_type"]; ok || getMap(block, "primary") != nil {
			st.Kind = "codex"
			st.Codex = CodexToState(block)
		} else {
			st.Kind = "openrouter"
			st.OpenRouter = OpenRouterToState(block)
		}
		snap.Providers = append(snap.Providers, st)
	}

	return snap
}

// LoadSnapshotFromDisk читает кэш из limits.json / фрагментов без сетевых запросов.
func LoadSnapshotFromDisk(cfg *config.Config, appDir string) *model.Snapshot {
	nowIso := NowIso()
	var agyRes *AgyResult
	if frag := loadCachedProviderBlocks(appDir, "agy"); frag != nil {
		agyRes = &AgyResult{
			Status:      getStr(frag, "status"),
			LastUpdated: getStr(frag, "last_updated"),
		}
		if b := agyBucketsFromBlock(frag); b != nil {
			agyRes.Buckets = *b
		}
	} else if root := loadLimitsJson(appDir); root != nil {
		if d := getMap(root, "data"); d != nil {
			if agyData := getMap(d, "agy"); agyData != nil {
				agyRes = &AgyResult{
					Status:      getStr(agyData, "status"),
					LastUpdated: getStr(agyData, "last_updated"),
				}
				if b := agyBucketsFromBlock(agyData); b != nil {
					agyRes.Buckets = *b
				}
			}
		}
	}

	providerBlocks := map[string]map[string]interface{}{}
	if cfg != nil {
		for _, p := range cfg.EffectiveProviders() {
			if !p.Enabled || p.Type == "agy" {
				continue
			}
			if block := loadCachedProvider(appDir, p.Id); block != nil {
				providerBlocks[p.Id] = block
			}
		}
	}

	if agyRes == nil && len(providerBlocks) == 0 {
		return nil
	}

	snap := &model.Snapshot{LastUpdated: nowIso}
	allStatuses := []string{}
	if agyRes != nil {
		allStatuses = append(allStatuses, agyRes.Status)
		snap.AgyStatus = agyRes.Status
		snap.AgyLastUpdated = agyRes.LastUpdated
		snap.Gemini5h = bucketFromMap(agyRes.Buckets.Gemini5h)
		snap.GeminiWeekly = bucketFromMap(agyRes.Buckets.GeminiWeekly)
		snap.Claude5h = bucketFromMap(agyRes.Buckets.Claude5h)
		snap.ClaudeWeekly = bucketFromMap(agyRes.Buckets.ClaudeWeekly)

		title := "GEMINI + CLAUDE/GPT"
		if cfg != nil {
			if p := cfg.FindProvider("agy"); p != nil && p.Title != "" {
				title = p.Title
			}
		}
		snap.Providers = append(snap.Providers, &model.ProviderState{
			Id: "agy", Kind: "agy", Title: title, Status: agyRes.Status, LastUpdated: agyRes.LastUpdated,
		})
	}

	for _, id := range orderedIds(providerBlocks) {
		block := providerBlocks[id]
		allStatuses = append(allStatuses, getStr(block, "status"))
		title := strings.ToUpper(id)
		if cfg != nil {
			if p := cfg.FindProvider(id); p != nil && p.Title != "" {
				title = p.Title
			}
		}
		st := &model.ProviderState{
			Id:          id,
			Title:       title,
			Status:      getStr(block, "status"),
			LastUpdated: getStr(block, "last_updated"),
		}
		if _, ok := block["limit_5h"]; ok {
			st.Kind = "kimi"
			st.Kimi = KimiToState(block)
		} else if _, ok := block["plan_type"]; ok || getMap(block, "primary") != nil {
			st.Kind = "codex"
			st.Codex = CodexToState(block)
		} else {
			st.Kind = "openrouter"
			st.OpenRouter = OpenRouterToState(block)
		}
		snap.Providers = append(snap.Providers, st)
	}

	rootStatus := "error"
	for _, s := range allStatuses {
		if s == "online" {
			rootStatus = "online"
			break
		}
	}
	snap.Status = rootStatus
	return snap
}

func orderedIds(m map[string]map[string]interface{}) []string {
	// стабильный порядок: kimi, openrouter, codex, затем остальные по алфавиту
	orderWeight := func(id string) int {
		switch id {
		case "kimi":
			return 1
		case "openrouter":
			return 2
		case "codex":
			return 3
		default:
			return 4
		}
	}
	var pref, rest []string
	for id := range m {
		switch id {
		case "kimi", "openrouter", "codex":
			pref = append(pref, id)
		default:
			rest = append(rest, id)
		}
	}
	// сортировка pref по orderWeight
	for i := 1; i < len(pref); i++ {
		for j := i; j > 0 && orderWeight(pref[j]) < orderWeight(pref[j-1]); j-- {
			pref[j], pref[j-1] = pref[j-1], pref[j]
		}
	}
	// простая сортировка вставками для rest
	for i := 1; i < len(rest); i++ {
		for j := i; j > 0 && rest[j] < rest[j-1]; j-- {
			rest[j], rest[j-1] = rest[j-1], rest[j]
		}
	}
	return append(pref, rest...)
}

// ---- чтение кэшированных блоков (фрагменты -> limits.json) ----

func fragmentPath(appDir, id string) string {
	return filepath.Join(appDir, "limits."+id+".json")
}

func saveFragment(appDir, id, nowIso string, block map[string]interface{}) map[string]interface{} {
	frag := map[string]interface{}{
		"status":       getStr(block, "status"),
		"last_updated": nowIso,
		"data":         block,
	}
	for k, v := range block {
		if k != "data" && k != "last_updated" {
			frag[k] = v
		}
	}
	WriteAtomicJson(fragmentPath(appDir, id), frag)
	return block
}

func loadCachedProvider(appDir, id string) map[string]interface{} {
	if frag := loadCachedProviderBlocks(appDir, id); frag != nil {
		return extractProviderBlock(id, frag)
	}
	if root := loadLimitsJson(appDir); root != nil {
		return extractProviderBlock(id, root)
	}
	return nil
}

// loadCachedProviderBlocks — сырой фрагмент (включая аги-обёртку с buckets).
func loadCachedProviderBlocks(appDir, id string) map[string]interface{} {
	if raw, err := os.ReadFile(fragmentPath(appDir, id)); err == nil {
		var m map[string]interface{}
		if json.Unmarshal(raw, &m) == nil {
			return m
		}
	}
	if raw, err := os.ReadFile(filepath.Join(appDir, "limits.json")); err == nil {
		var m map[string]interface{}
		if json.Unmarshal(raw, &m) == nil {
			return m
		}
	}
	return nil
}

// extractProviderBlock — достаёт блок провайдера из фрагмента/корня.
func extractProviderBlock(id string, payload map[string]interface{}) map[string]interface{} {
	if payload == nil {
		return nil
	}
	d := getMap(payload, "data")
	if id == "kimi" {
		if d != nil {
			if v := getMap(d, "kimi"); v != nil {
				return v
			}
			if getMap(d, "limit_5h") != nil {
				return d
			}
		}
		if v := getMap(payload, "kimi"); v != nil {
			return v
		}
		if getMap(payload, "limit_5h") != nil {
			return payload
		}
		return nil
	}
	if id == "openrouter" {
		if d != nil {
			if v := getMap(d, "openrouter"); v != nil {
				return v
			}
			if hasNonNull(d, "limit") || hasNonNull(d, "usage") {
				return d
			}
		}
		if v := getMap(payload, "openrouter"); v != nil {
			return v
		}
		if hasNonNull(payload, "limit") || hasNonNull(payload, "usage") {
			return payload
		}
		return nil
	}
	if id == "codex" {
		if d != nil {
			if v := getMap(d, "codex"); v != nil {
				return v
			}
			if getMap(d, "primary") != nil {
				return d
			}
		}
		if v := getMap(payload, "codex"); v != nil {
			return v
		}
		if getMap(payload, "primary") != nil {
			return payload
		}
		return nil
	}
	if id == "agy" {
		if d != nil && getMap(d, "gemini_5h") != nil {
			return agyExtractFromData(d, payload)
		}
		if getMap(payload, "gemini_5h") != nil {
			return agyExtractFromData(payload, payload)
		}
		return nil
	}
	// произвольный id
	if d != nil {
		if v := getMap(d, id); v != nil {
			return v
		}
		if getMap(d, "limit_5h") != nil || hasNonNull(d, "limit") || hasNonNull(d, "usage") {
			return d
		}
	}
	if v := getMap(payload, id); v != nil {
		return v
	}
	if getMap(payload, "limit_5h") != nil || hasNonNull(payload, "limit") || hasNonNull(payload, "usage") {
		return payload
	}
	return nil
}

func agyExtractFromData(d, payload map[string]interface{}) map[string]interface{} {
	buckets := map[string]interface{}{}
	for _, k := range []string{"gemini_5h", "gemini_weekly", "claude_5h", "claude_weekly"} {
		if v := getMap(d, k); v != nil {
			buckets[k] = v
		}
	}
	return map[string]interface{}{
		"buckets":      buckets,
		"status":       getStr(payload, "status"),
		"last_updated": getStr(d, "last_updated"),
	}
}

func loadLimitsJson(appDir string) map[string]interface{} {
	raw, err := os.ReadFile(filepath.Join(appDir, "limits.json"))
	if err != nil {
		return nil
	}
	var m map[string]interface{}
	if json.Unmarshal(raw, &m) != nil {
		return nil
	}
	return m
}
