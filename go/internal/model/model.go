package model

// Bucket — одно окно лимита (kimi-окно или аги-бакет).
type Bucket struct {
	UsedPercent  float64 `json:"used_percent"`
	UsedFraction float64 `json:"used_fraction"`
	ResetIn      string  `json:"reset_in"`
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

// ProviderState — состояние одного провайдера (включая кастомные id).
type ProviderState struct {
	Id          string
	Title       string
	Kind        string // kimi | openrouter | agy | codex
	Status      string
	LastUpdated string
	Kimi        *KimiInfo
	OpenRouter  *OpenRouterInfo
	Codex       *CodexInfo
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
