package config

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"strings"
	"sync"
)

var (
	logMu   sync.Mutex
	logFunc = func(msg string) {
		log.Println(msg)
	}
)

// SetLogFunc переопределяет функцию логирования для модуля config.
func SetLogFunc(fn func(string)) {
	logMu.Lock()
	defer logMu.Unlock()
	logFunc = fn
}

// Log отправляет сообщение в текущий логгер.
func Log(msg string) {
	logMu.Lock()
	fn := logFunc
	logMu.Unlock()
	if fn != nil {
		fn(msg)
	}
}

// StripBOM срезает UTF-8 BOM (\xef\xbb\xbf), если он есть в начале среза байт.
func StripBOM(b []byte) []byte {
	return bytes.TrimPrefix(b, []byte("\xef\xbb\xbf"))
}

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
	Path               string
	ClickThrough       bool
	ClickThroughHotkey string
	Providers          []Provider // пусто = дефолты
	// Limits — какие окна (периодные лимиты) показывать для каждого типа:
	// {"kimi":{"7d":false},"opencode":{"5h":false},...}. Отсутствующая запись
	// или отсутствующий ключ = показывать; false = строка скрыта.
	Limits map[string]map[string]bool
}

// ShowWindow — показывать ли окно key у провайдера типа typeName.
// Ключи регистронезависимы; неизвестный тип/ключ не скрывает ничего.
func (c *Config) ShowWindow(typeName, key string) bool {
	if c == nil || len(c.Limits) == 0 {
		return true
	}
	byType, ok := c.Limits[strings.ToLower(strings.TrimSpace(typeName))]
	if !ok {
		return true
	}
	v, ok := byType[strings.ToLower(strings.TrimSpace(key))]
	if !ok {
		return true
	}
	return v
}

func Defaults() []Provider {
	return []Provider{
		{Id: "kimi", Type: "kimi", Title: "KIMI", BaseUrl: "https://api.kimi.com/coding/v1", ApiKeyEnv: "KIMI_API_KEY", Enabled: true},
		{Id: "openrouter", Type: "openrouter", Title: "OPENROUTER", BaseUrl: "https://openrouter.ai/api/v1", ApiKeyEnv: "OPENROUTER_API_KEY", Enabled: true},
		{Id: "opencode", Type: "opencode", Title: "OPENCODE GO", BaseUrl: "https://opencode.ai/zen/go/v1", ApiKeyEnv: "OPENCODE_GO_API_KEY", Enabled: true},
		{Id: "agy", Type: "agy", Title: "GEMINI + CLAUDE/GPT", Enabled: true},
	}
}

func (c *Config) EffectiveProviders() []Provider {
	if len(c.Providers) == 0 {
		return Defaults()
	}
	return c.Providers
}

func (c *Config) FindProvider(id string) *Provider {
	for _, p := range c.EffectiveProviders() {
		if strings.EqualFold(p.Id, id) {
			cp := p
			return &cp
		}
	}
	return nil
}

// Load читает overlay.config.json (нефатально: при отсутствии/ошибке — дефолты).
func Load(path string) *Config {
	cfg := &Config{
		Path:               path,
		ClickThrough:       true,
		ClickThroughHotkey: "Ctrl+Alt+L",
	}
	if path == "" {
		return cfg
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		if !os.IsNotExist(err) {
			Log(fmt.Sprintf("[config] parse error in %s: %v, using defaults", path, err))
		}
		return cfg
	}
	raw = StripBOM(raw)
	var root map[string]interface{}
	if err := json.Unmarshal(raw, &root); err != nil {
		Log(fmt.Sprintf("[config] parse error in %s: %v, using defaults", path, err))
		return cfg
	}
	if v, ok := root["clickThrough"].(bool); ok {
		cfg.ClickThrough = v
	}
	if v, ok := root["clickThroughHotkey"].(string); ok && strings.TrimSpace(v) != "" {
		cfg.ClickThroughHotkey = strings.TrimSpace(v)
	}
	if rawProviders, ok := root["providers"].([]interface{}); ok {
		cfg.Providers = parseProviders(rawProviders)
	}
	if rawLimits, ok := root["limits"].(map[string]interface{}); ok {
		cfg.Limits = parseLimits(rawLimits)
	}
	return cfg
}

func (c *Config) SaveClickThrough(enabled bool) {
	c.ClickThrough = enabled
	if c.Path == "" {
		return
	}
	raw, err := os.ReadFile(c.Path)
	var root map[string]interface{}
	if err == nil {
		raw = StripBOM(raw)
		_ = json.Unmarshal(raw, &root)
	}
	if root == nil {
		root = map[string]interface{}{}
	}
	root["clickThrough"] = enabled
	out, err := json.MarshalIndent(root, "", "  ")
	if err == nil {
		_ = os.WriteFile(c.Path, out, 0644)
	}
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
		case "kimi", "openrouter", "agy", "codex", "opencode":
		default:
			Log(fmt.Sprintf("[config] providers: неизвестный type '%s' у записи '%s' — запись пропущена (новый провайдер = новый тип в коде)", p.Type, p.Id))
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

// parseLimits разбирает "limits": {"<type>": {"<window>": false}}.
// Неизвестные типы и небулевы значения игнорируются без ошибки (старый конфиг
// обязан грузиться молча).
func parseLimits(raw map[string]interface{}) map[string]map[string]bool {
	out := map[string]map[string]bool{}
	for typeName, rawType := range raw {
		byWindow, ok := rawType.(map[string]interface{})
		if !ok {
			continue
		}
		t := strings.ToLower(strings.TrimSpace(typeName))
		if t == "" {
			continue
		}
		acc := map[string]bool{}
		for window, rawVal := range byWindow {
			v, ok := rawVal.(bool)
			if !ok {
				continue
			}
			w := strings.ToLower(strings.TrimSpace(window))
			if w == "" {
				continue
			}
			acc[w] = v
		}
		if len(acc) > 0 {
			out[t] = acc
		}
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
