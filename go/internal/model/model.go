package model

import "strings"

// Bucket — одно окно лимита (kimi-окно или аги-бакет).
type Bucket struct {
	UsedPercent  float64 `json:"used_percent"`
	UsedFraction float64 `json:"used_fraction"`
	ResetIn      string  `json:"reset_in"`
	// Placeholder — true когда значение является заглушкой (нет измеренных
	// данных), а не реальным «0%% использовано». Слой отображения обязан
	// рисовать «--»/«нет данных» вместо выдуманного процента.
	Placeholder bool `json:"placeholder,omitempty"`
}

type KimiInfo struct {
	Status      string
	LastUpdated string
	Limit5h     Bucket
	Limit7d     Bucket
}

type OpenRouterInfo struct {
	Status         string
	LastUpdated    string
	Limit          float64
	Usage          float64
	UsagePeriod    float64
	UsageTotal     float64
	LimitRemaining float64
	UsedPercent    float64
	UsedFraction   float64
	ResetIn        string
	LimitReset     string
	FreeDailyUsed  int
	FreeDailyLimit int
}

type CodexInfo struct {
	Status        string
	LastUpdated   string
	PlanType      string
	Balance       string
	Primary       Bucket
	PrimaryName   string
	PrimaryMin    int
	Secondary     *Bucket
	SecondaryName string
	SecondaryMin  int
}

// Window — именованное окно лимита с денежными границами (OpenCode Go).
type Window struct {
	Name         string
	Bucket
	LimitUSD     float64
	RemainingUSD float64
	Limited      bool
}

// OpenCodeInfo — OpenCode Go: три долларовых окна (5ч / 7д / 30д).
type OpenCodeInfo struct {
	Status      string
	LastUpdated string
	Plan        string
	Rolling     Window
	Weekly      Window
	Monthly     Window
}

// LimitKey — ключ настройки limits.<type> для этого окна: 30d / 7d / 5h.
func (w Window) LimitKey() string {
	switch w.Name {
	case "30д":
		return "30d"
	case "7д":
		return "7d"
	case "5ч":
		return "5h"
	}
	return strings.ToLower(w.Name)
}

// Windows — окна в порядке отображения (как у остальных карточек:
// сначала длинное окно, затем короткие): 30д, 7д, 5ч.
func (o *OpenCodeInfo) Windows() []Window {
	return []Window{o.Monthly, o.Weekly, o.Rolling}
}

// ProviderState — состояние одного провайдера (включая кастомные id).
type ProviderState struct {
	Id          string
	Title       string
	Kind        string // kimi | openrouter | agy | codex | opencode
	Status      string
	LastUpdated string
	Kimi        *KimiInfo
	OpenRouter  *OpenRouterInfo
	Codex       *CodexInfo
	OpenCode    *OpenCodeInfo
}

// Snapshot — результат одного цикла фетча.
type Snapshot struct {
	Status        string
	LastUpdated   string
	AgyStatus     string
	AgyLastUpdated string
	Gemini5h      Bucket
	GeminiWeekly  Bucket
	Claude5h      Bucket
	ClaudeWeekly  Bucket
	Providers     []*ProviderState
}

func (s *Snapshot) Find(id string) *ProviderState {
	for _, p := range s.Providers {
		if p.Id == id {
			return p
		}
	}
	return nil
}
