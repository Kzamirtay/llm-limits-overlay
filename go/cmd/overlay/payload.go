package main

import (
	"fmt"
	"math"

	"llmlimits/internal/config"
	"llmlimits/internal/model"
)

type BucketView struct {
	UsedPercent  float64 `json:"used_percent"`
	UsedFraction float64 `json:"used_fraction"`
	ResetIn      string  `json:"reset_in"`
}

type ProviderView struct {
	Id           string      `json:"id"`
	Type         string      `json:"type"` // "kimi" | "openrouter" | "agy"
	Title        string      `json:"title"`
	Status       string      `json:"status"` // "online" | "stale" | "no_key" | "error"
	LastUpdated  string      `json:"last_updated"`
	Balance      string      `json:"balance,omitempty"`
	Limit7d      *BucketView `json:"limit_7d,omitempty"`
	Limit5h      *BucketView `json:"limit_5h,omitempty"`
	MainLimit    *BucketView `json:"main_limit,omitempty"`
	FreeDaily    *BucketView `json:"free_daily,omitempty"`
	FreeLabel    string      `json:"free_label,omitempty"`
	GeminiWeekly *BucketView `json:"gemini_weekly,omitempty"`
	Gemini5h     *BucketView `json:"gemini_5h,omitempty"`
	ClaudeWeekly *BucketView `json:"claude_weekly,omitempty"`
	Claude5h     *BucketView `json:"claude_5h,omitempty"`
}

type OverlayPayload struct {
	Status    string         `json:"status"`
	Providers []ProviderView `json:"providers"`
}

func buildOverlayPayload(snap *model.Snapshot, cfg *config.Config) OverlayPayload {
	payload := OverlayPayload{
		Status:    "error",
		Providers: []ProviderView{},
	}
	if snap == nil {
		return payload
	}
	payload.Status = snap.Status

	for _, p := range cfg.EffectiveProviders() {
		if !p.Enabled {
			continue
		}
		title := p.Title
		if title == "" {
			title = p.Id
		}

		if p.Type == "agy" {
			pv := ProviderView{
				Id:          p.Id,
				Type:        "agy",
				Title:       title,
				Status:      snap.AgyStatus,
				LastUpdated: snap.AgyLastUpdated,
				GeminiWeekly: &BucketView{
					UsedPercent:  snap.GeminiWeekly.UsedPercent,
					UsedFraction: snap.GeminiWeekly.UsedFraction,
					ResetIn:      snap.GeminiWeekly.ResetIn,
				},
				Gemini5h: &BucketView{
					UsedPercent:  snap.Gemini5h.UsedPercent,
					UsedFraction: snap.Gemini5h.UsedFraction,
					ResetIn:      snap.Gemini5h.ResetIn,
				},
				ClaudeWeekly: &BucketView{
					UsedPercent:  snap.ClaudeWeekly.UsedPercent,
					UsedFraction: snap.ClaudeWeekly.UsedFraction,
					ResetIn:      snap.ClaudeWeekly.ResetIn,
				},
				Claude5h: &BucketView{
					UsedPercent:  snap.Claude5h.UsedPercent,
					UsedFraction: snap.Claude5h.UsedFraction,
					ResetIn:      snap.Claude5h.ResetIn,
				},
			}
			payload.Providers = append(payload.Providers, pv)
			continue
		}

		st := snap.Find(p.Id)
		if st == nil {
			pv := ProviderView{
				Id:          p.Id,
				Type:        p.Type,
				Title:       title,
				Status:      "no_data",
				LastUpdated: "",
			}
			payload.Providers = append(payload.Providers, pv)
			continue
		}

		pv := ProviderView{
			Id:          p.Id,
			Type:        p.Type,
			Title:       title,
			Status:      st.Status,
			LastUpdated: st.LastUpdated,
		}

		if p.Type == "openrouter" {
			if st.OpenRouter != nil {
				if st.OpenRouter.Limit > 0 {
					rem := math.Max(0.0, st.OpenRouter.LimitRemaining)
					pv.Balance = fmt.Sprintf("ост. $%.2f", rem)
				}
				pv.MainLimit = &BucketView{
					UsedPercent:  st.OpenRouter.UsedPercent,
					UsedFraction: st.OpenRouter.UsedFraction,
					ResetIn:      st.OpenRouter.ResetIn,
				}
				if st.OpenRouter.FreeDailyLimit > 0 {
					pv.FreeLabel = fmt.Sprintf("free: %d/%d", st.OpenRouter.FreeDailyUsed, st.OpenRouter.FreeDailyLimit)
					pct := float64(st.OpenRouter.FreeDailyUsed) / float64(st.OpenRouter.FreeDailyLimit) * 100.0
					pv.FreeDaily = &BucketView{
						UsedPercent:  pct,
						UsedFraction: pct / 100.0,
						ResetIn:      "",
					}
				}
			}
		} else {
			// kimi
			if st.Kimi != nil {
				pv.Limit7d = &BucketView{
					UsedPercent:  st.Kimi.Limit7d.UsedPercent,
					UsedFraction: st.Kimi.Limit7d.UsedFraction,
					ResetIn:      st.Kimi.Limit7d.ResetIn,
				}
				pv.Limit5h = &BucketView{
					UsedPercent:  st.Kimi.Limit5h.UsedPercent,
					UsedFraction: st.Kimi.Limit5h.UsedFraction,
					ResetIn:      st.Kimi.Limit5h.ResetIn,
				}
			}
		}

		payload.Providers = append(payload.Providers, pv)
	}

	return payload
}
