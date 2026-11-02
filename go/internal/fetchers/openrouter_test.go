package fetchers

import (
	"encoding/json"
	"testing"
)

func parseJSONData(t *testing.T, rawJSON string) map[string]interface{} {
	t.Helper()
	var root map[string]interface{}
	if err := json.Unmarshal([]byte(rawJSON), &root); err != nil {
		t.Fatalf("failed to unmarshal test JSON: %v", err)
	}
	data, ok := root["data"].(map[string]interface{})
	if !ok {
		t.Fatalf("test JSON missing data object")
	}
	return data
}

// 1. Weekly with large lifetime usage (reproduces the user bug)
// limit=10, limit_remaining=8.9781071, limit_reset=weekly, usage=6.31130951 ALL TIME,
// usage_weekly=1.0218929, effective_budget.spend_usd=1.0218929.
// Expected: 10.2%, NOT 63.1%.
func TestOpenRouter_WeeklyWithLargeLifetimeUsage(t *testing.T) {
	raw := `{
		"data": {
			"label": "sk-or-v1-my-key",
			"limit": 10,
			"limit_remaining": 8.978107099999999,
			"limit_reset": "weekly",
			"usage": 6.31130951,
			"usage_daily": 1.0218929,
			"usage_weekly": 1.0218929,
			"usage_monthly": 1.73094547,
			"budgets": [
				{
					"scope": "key_limit",
					"limit_usd": 10,
					"spend_usd": 1.0218929,
					"remaining_usd": 8.978107099999999,
					"reset_interval": "weekly",
					"resets_at": "2026-11-09T00:00:00.000Z",
					"include_byok": false
				}
			],
			"effective_budget": {
				"scope": "key_limit",
				"limit_usd": 10,
				"spend_usd": 1.0218929,
				"remaining_usd": 8.978107099999999,
				"reset_interval": "weekly",
				"resets_at": "2026-11-09T00:00:00.000Z",
				"include_byok": false
			},
			"free_model_daily_requests": {
				"used": 12,
				"limit": 1000
			}
		}
	}`

	data := parseJSONData(t, raw)
	res := parseOpenRouterData(data, "2026-11-02 10:00:00")

	// Verify all fields refer to the same active budget
	if got := res["used_percent"].(float64); got != 10.2 {
		t.Errorf("used_percent = %v, want 10.2 (got lifetime-polluted percent)", got)
	}
	if got := res["used_fraction"].(float64); got != 0.1022 {
		t.Errorf("used_fraction = %v, want 0.1022", got)
	}
	if got := res["limit"].(float64); got != 10.0 {
		t.Errorf("limit = %v, want 10.0", got)
	}
	if got := res["limit_remaining"].(float64); got != 8.98 {
		t.Errorf("limit_remaining = %v, want 8.98", got)
	}
	// Contract: usage preserves all-time lifetime usage
	if got := res["usage"].(float64); got != 6.3113 {
		t.Errorf("usage = %v, want 6.3113", got)
	}
	if got := res["usage_period"].(float64); got != 1.0219 {
		t.Errorf("usage_period = %v, want 1.0219", got)
	}
	if got := res["limit_reset"].(string); got != "weekly" {
		t.Errorf("limit_reset = %v, want weekly", got)
	}
	if got := res["resets_at"].(string); got != "2026-11-09T00:00:00.000Z" {
		t.Errorf("resets_at = %v, want 2026-11-09T00:00:00.000Z", got)
	}
}

// 2. Daily and Monthly periodic caps
func TestOpenRouter_DailyAndMonthly(t *testing.T) {
	t.Run("daily", func(t *testing.T) {
		raw := `{
			"data": {
				"limit": 5.0,
				"limit_remaining": 4.5,
				"limit_reset": "daily",
				"usage": 80.0,
				"usage_daily": 0.5,
				"effective_budget": {
					"scope": "daily_cap",
					"limit_usd": 5.0,
					"spend_usd": 0.5,
					"remaining_usd": 4.5,
					"reset_interval": "daily",
					"resets_at": "2026-11-03T00:00:00.000Z"
				}
			}
		}`
		res := parseOpenRouterData(parseJSONData(t, raw), "2026-11-02 10:00:00")
		if got := res["used_percent"].(float64); got != 10.0 {
			t.Errorf("daily used_percent = %v, want 10.0", got)
		}
		if got := res["limit_reset"].(string); got != "daily" {
			t.Errorf("daily limit_reset = %v, want daily", got)
		}
	})

	t.Run("monthly", func(t *testing.T) {
		raw := `{
			"data": {
				"limit": 100.0,
				"limit_remaining": 75.0,
				"limit_reset": "monthly",
				"usage": 1500.0,
				"usage_monthly": 25.0,
				"effective_budget": {
					"scope": "monthly_cap",
					"limit_usd": 100.0,
					"spend_usd": 25.0,
					"remaining_usd": 75.0,
					"reset_interval": "monthly",
					"resets_at": "2026-12-01T00:00:00.000Z"
				}
			}
		}`
		res := parseOpenRouterData(parseJSONData(t, raw), "2026-11-02 10:00:00")
		if got := res["used_percent"].(float64); got != 25.0 {
			t.Errorf("monthly used_percent = %v, want 25.0", got)
		}
		if got := res["limit_reset"].(string); got != "monthly" {
			t.Errorf("monthly limit_reset = %v, want monthly", got)
		}
	})
}

// 3. Non-reset lifetime budget
func TestOpenRouter_NonResetLifetime(t *testing.T) {
	raw := `{
		"data": {
			"limit": 50.0,
			"limit_remaining": 25.0,
			"limit_reset": "none",
			"usage": 25.0,
			"effective_budget": {
				"scope": "key_limit",
				"limit_usd": 50.0,
				"spend_usd": 25.0,
				"remaining_usd": 25.0,
				"reset_interval": "none",
				"resets_at": null
			}
		}
	}`
	res := parseOpenRouterData(parseJSONData(t, raw), "2026-11-02 10:00:00")
	if got := res["used_percent"].(float64); got != 50.0 {
		t.Errorf("lifetime used_percent = %v, want 50.0", got)
	}
	if got := res["resets_at"].(string); got != "" {
		t.Errorf("lifetime resets_at = %q, want empty", got)
	}
	if got := res["reset_in"].(string); got != "--" {
		t.Errorf("lifetime reset_in = %q, want --", got)
	}
}

// 4. effective_budget differs from budgets[0] / key limit
// budgets[0] is a $100 monthly budget with reset next month.
// effective_budget is a $10 daily budget that resets tomorrow!
// ALL fields must come from effective_budget, NOT budgets[0].
func TestOpenRouter_EffectiveBudgetDiffersFromBudgets0(t *testing.T) {
	raw := `{
		"data": {
			"limit": 100.0,
			"limit_remaining": 95.0,
			"limit_reset": "monthly",
			"usage": 5.0,
			"budgets": [
				{
					"scope": "key_limit",
					"limit_usd": 100.0,
					"spend_usd": 5.0,
					"remaining_usd": 95.0,
					"reset_interval": "monthly",
					"resets_at": "2026-12-31T00:00:00.000Z"
				},
				{
					"scope": "team_daily",
					"limit_usd": 10.0,
					"spend_usd": 8.0,
					"remaining_usd": 2.0,
					"reset_interval": "daily",
					"resets_at": "2026-11-03T00:00:00.000Z"
				}
			],
			"effective_budget": {
				"scope": "team_daily",
				"limit_usd": 10.0,
				"spend_usd": 8.0,
				"remaining_usd": 2.0,
				"reset_interval": "daily",
				"resets_at": "2026-11-03T00:00:00.000Z"
			}
		}
	}`
	res := parseOpenRouterData(parseJSONData(t, raw), "2026-11-02 10:00:00")
	if got := res["limit"].(float64); got != 10.0 {
		t.Errorf("limit = %v, want 10.0 (must not pick budgets[0] limit)", got)
	}
	if got := res["limit_remaining"].(float64); got != 2.0 {
		t.Errorf("limit_remaining = %v, want 2.0 (must not pick budgets[0] remaining)", got)
	}
	if got := res["used_percent"].(float64); got != 80.0 {
		t.Errorf("used_percent = %v, want 80.0", got)
	}
	if got := res["limit_reset"].(string); got != "daily" {
		t.Errorf("limit_reset = %v, want daily", got)
	}
	if got := res["resets_at"].(string); got != "2026-11-03T00:00:00.000Z" {
		t.Errorf("resets_at = %v, want 2026-11-03T00:00:00.000Z (must not pick budgets[0] resets_at)", got)
	}
}

// 5. Zero spend
func TestOpenRouter_ZeroSpend(t *testing.T) {
	raw := `{
		"data": {
			"limit": 10.0,
			"limit_remaining": 10.0,
			"effective_budget": {
				"limit_usd": 10.0,
				"spend_usd": 0.0,
				"remaining_usd": 10.0,
				"reset_interval": "weekly",
				"resets_at": "2026-11-09T00:00:00.000Z"
			}
		}
	}`
	res := parseOpenRouterData(parseJSONData(t, raw), "2026-11-02 10:00:00")
	if got := res["used_percent"].(float64); got != 0.0 {
		t.Errorf("used_percent = %v, want 0.0", got)
	}
	if got := res["used_fraction"].(float64); got != 0.0 {
		t.Errorf("used_fraction = %v, want 0.0", got)
	}
	if got := res["limit_remaining"].(float64); got != 10.0 {
		t.Errorf("limit_remaining = %v, want 10.0", got)
	}
}

// 6. Null / unlimited limit
func TestOpenRouter_NullUnlimitedLimit(t *testing.T) {
	raw := `{
		"data": {
			"limit": null,
			"limit_remaining": null,
			"limit_reset": null,
			"usage": 15.0,
			"effective_budget": {
				"limit_usd": null,
				"spend_usd": 15.0,
				"remaining_usd": null,
				"reset_interval": null,
				"resets_at": null
			}
		}
	}`
	res := parseOpenRouterData(parseJSONData(t, raw), "2026-11-02 10:00:00")
	if got := res["limit"].(float64); got != 0.0 {
		t.Errorf("limit = %v, want 0.0", got)
	}
	if got := res["used_percent"].(float64); got != 0.0 {
		t.Errorf("used_percent = %v, want 0.0", got)
	}
	if got := res["used_fraction"].(float64); got != 0.0 {
		t.Errorf("used_fraction = %v, want 0.0", got)
	}
}

// 7. Zero budget limit (limit = 0.0)
func TestOpenRouter_ZeroBudget(t *testing.T) {
	t.Run("zero_budget_zero_spend", func(t *testing.T) {
		raw := `{
			"data": {
				"limit": 0.0,
				"limit_remaining": 0.0,
				"effective_budget": {
					"limit_usd": 0.0,
					"spend_usd": 0.0,
					"remaining_usd": 0.0
				}
			}
		}`
		res := parseOpenRouterData(parseJSONData(t, raw), "2026-11-02 10:00:00")
		if got := res["used_percent"].(float64); got != 0.0 {
			t.Errorf("used_percent = %v, want 0.0 (must not invent 100 percent)", got)
		}
		if got := res["used_fraction"].(float64); got != 0.0 {
			t.Errorf("used_fraction = %v, want 0.0", got)
		}
	})

	t.Run("zero_budget_with_spend", func(t *testing.T) {
		raw := `{
			"data": {
				"limit": 0.0,
				"limit_remaining": 0.0,
				"effective_budget": {
					"limit_usd": 0.0,
					"spend_usd": 2.5,
					"remaining_usd": 0.0
				}
			}
		}`
		res := parseOpenRouterData(parseJSONData(t, raw), "2026-11-02 10:00:00")
		if got := res["used_percent"].(float64); got != 100.0 {
			t.Errorf("used_percent = %v, want 100.0 (limit exceeded)", got)
		}
		if got := res["used_fraction"].(float64); got != 1.0 {
			t.Errorf("used_fraction = %v, want 1.0", got)
		}
	})
}

// 8. Fallback without effective_budget
func TestOpenRouter_FallbackWithoutEffectiveBudget(t *testing.T) {
	raw := `{
		"data": {
			"limit": 10.0,
			"limit_remaining": 8.0,
			"limit_reset": "weekly",
			"usage": 200.0,
			"usage_weekly": 2.0,
			"budgets": [
				{
					"scope": "key_limit",
					"reset_interval": "weekly",
					"resets_at": "2026-11-09T00:00:00.000Z"
				}
			]
		}
	}`
	res := parseOpenRouterData(parseJSONData(t, raw), "2026-11-02 10:00:00")
	if got := res["used_percent"].(float64); got != 20.0 {
		t.Errorf("used_percent = %v, want 20.0 (must not divide lifetime usage)", got)
	}
	if got := res["limit"].(float64); got != 10.0 {
		t.Errorf("limit = %v, want 10.0", got)
	}
	if got := res["limit_remaining"].(float64); got != 8.0 {
		t.Errorf("limit_remaining = %v, want 8.0", got)
	}
	if got := res["resets_at"].(string); got != "2026-11-09T00:00:00.000Z" {
		t.Errorf("resets_at = %v, want 2026-11-09T00:00:00.000Z", got)
	}
}

// 9. Fallback with include_byok false vs true (when limit_remaining is absent)
func TestOpenRouter_FallbackBYOK(t *testing.T) {
	t.Run("byok_false", func(t *testing.T) {
		raw := `{
			"data": {
				"limit": 10.0,
				"limit_reset": "weekly",
				"usage": 100.0,
				"usage_weekly": 3.0,
				"byok_usage_weekly": 2.0,
				"include_byok": false
			}
		}`
		res := parseOpenRouterData(parseJSONData(t, raw), "2026-11-02 10:00:00")
		if got := res["used_percent"].(float64); got != 30.0 {
			t.Errorf("used_percent = %v, want 30.0 (spend=3.0)", got)
		}
	})

	t.Run("byok_true", func(t *testing.T) {
		raw := `{
			"data": {
				"limit": 10.0,
				"limit_reset": "weekly",
				"usage": 100.0,
				"usage_weekly": 3.0,
				"byok_usage_weekly": 2.0,
				"include_byok": true
			}
		}`
		res := parseOpenRouterData(parseJSONData(t, raw), "2026-11-02 10:00:00")
		if got := res["used_percent"].(float64); got != 50.0 {
			t.Errorf("used_percent = %v, want 50.0 (spend=3.0+2.0=5.0)", got)
		}
	})
}
