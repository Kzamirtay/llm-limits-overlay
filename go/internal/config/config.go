package config

import (
	"encoding/json"
	"log"
	"os"
	"strings"
)

// Provider — запись из overlay.config.json ("providers": [...]).
// Только явные типы: kimi | openrouter | agy. Неизвестный type пропускается.
type Provider struct {
	Id       string
	Type     string
	Title    string
	BaseUrl  string
	ApiKeyEnv string
	ApiKey   string
	Enabled  bool
}

type Config struct {
	Path          string
	TimeoutSec    float64
	Providers     []Provider // пусто = дефолты
}

func Defaults() []Provider {
	return []Provider{
		{Id: "kimi", Type: "kimi", Title: "KIMI", BaseUrl: "https://api.kimi.com/coding/v1", ApiKeyEnv: "KIMI_API_KEY", Enabled: true},
		{Id: "openrouter", Type: "openrouter", Title: "OPENROUTER", BaseUrl: "https://openrouter.ai/api/v1", ApiKeyEnv: "OPENROUTER_API_KEY", Enabled: true},
		{Id: "agy", Type: "agy", Title: "GEMINI + CLAUDE/GPT", Enabled: true},
	}
}

func (c *Config) EffectiveProviders() []Provider {
	if len(c.Providers) == 0 {
		return Defaults()
	}
	return c.Providers
}

// Load читает overlay.config.json (нефатально: при отсутствии/ошибке — дефолты).
func Load(path string) *Config {
	cfg := &Config{Path: path}
	raw, err := os.ReadFile(path)
	if err != nil {
		return cfg
	}
	var root map[string]interface{}
	if err := json.Unmarshal(raw, &root); err != nil {
		return cfg
	}
	if v, ok := root["timeoutSeconds"].(float64); ok {
		cfg.TimeoutSec = v
	}
	if rawProviders, ok := root["providers"].([]interface{}); ok {
		cfg.Providers = parseProviders(rawProviders)
	}
	return cfg
}

func parseProviders(list []interface{}) []Provider {
	var out []Provider
	autoIdx := 0
	for _, item := range list {
		d, ok := item.(map[string]interface{})
		if !ok {
			continue
		}
		p := Provider{Enabled: true}
		p.Id = strings.ToLower(strings.TrimSpace(getStr(d, "id")))
		if p.Id == "" {
			autoIdx++
			p.Id = "provider" + itoa(autoIdx)
		}
		p.Type = strings.ToLower(strings.TrimSpace(getStr(d, "type")))
		if p.Type == "" {
			p.Type = "kimi"
		}
		switch p.Type {
		case "kimi", "openrouter", "agy":
		default:
			log.Printf("[config] providers: неизвестный type '%s' у записи '%s' — запись пропущена (новый провайдер = новый тип в коде)", p.Type, p.Id)
			continue
		}
		p.Title = getStr(d, "title")
		if p.Title == "" {
			p.Title = strings.ToUpper(p.Id)
		}
		p.BaseUrl = strings.TrimRight(strings.TrimSpace(getStr(d, "baseUrl")), "/")
		p.ApiKeyEnv = strings.TrimSpace(getStr(d, "apiKeyEnv"))
		p.ApiKey = strings.TrimSpace(getStr(d, "apiKey"))
		if v, ok := d["enabled"]; ok {
			if b, ok2 := v.(bool); ok2 {
				p.Enabled = b
			}
		}
		out = append(out, p)
	}
	return out
}

func getStr(d map[string]interface{}, key string) string {
	if v, ok := d[key].(string); ok {
		return v
	}
	return ""
}

func itoa(n int) string {
	return string(rune('0'+n%10))
}
