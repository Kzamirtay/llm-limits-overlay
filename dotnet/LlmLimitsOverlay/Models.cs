using System;
using System.Collections.Generic;

namespace LlmLimitsOverlay {
    public class BucketInfo {
        public double Percent { get; set; }
        public double Fraction { get; set; }
        public string ResetIn { get; set; }

        public BucketInfo() {
            Percent = 0.0;
            Fraction = 0.0;
            ResetIn = "--";
        }
    }

    public class OpenRouterInfo {
        public string Status { get; set; }
        public string LastUpdated { get; set; }
        public double Limit { get; set; }
        public double Usage { get; set; }
        public double LimitRemaining { get; set; }
        public double UsedPercent { get; set; }
        public double UsedFraction { get; set; }
        public string ResetIn { get; set; }
        public string LimitReset { get; set; }
        public int FreeDailyLimit { get; set; }
        public int FreeDailyUsed { get; set; }

        public OpenRouterInfo() {
            Status = "online";
            LastUpdated = "";
            Limit = 0.0;
            Usage = 0.0;
            LimitRemaining = 0.0;
            UsedPercent = 0.0;
            UsedFraction = 0.0;
            ResetIn = "--";
            LimitReset = "monthly";
            FreeDailyLimit = 0;
            FreeDailyUsed = 0;
        }
    }

    public class KimiInfo {
        public string Status { get; set; }
        public string LastUpdated { get; set; }
        public BucketInfo Limit5h { get; set; }
        public BucketInfo Limit7d { get; set; }

        public KimiInfo() {
            Status = "online";
            LastUpdated = "";
            Limit5h = new BucketInfo();
            Limit7d = new BucketInfo();
        }
    }

    // Состояние одного провайдера (включая кастомные id из конфига)
    public class ProviderState {
        public string Id = "";
        public string Title = "";
        public string Kind = "kimi"; // kimi | openrouter | agy
        public string Status = "online";
        public string LastUpdated = "";
        public KimiInfo Kimi = null;
        public OpenRouterInfo OpenRouter = null;
    }

    public class QuotaSnapshot {
        public string Status { get; set; }
        public string LastUpdated { get; set; }
        public string AgyStatus { get; set; }
        public string AgyLastUpdated { get; set; }
        public BucketInfo Gemini5h { get; set; }
        public BucketInfo GeminiWeekly { get; set; }
        public BucketInfo Claude5h { get; set; }
        public BucketInfo ClaudeWeekly { get; set; }
        public OpenRouterInfo OpenRouter { get; set; }
        public KimiInfo Kimi { get; set; }
        // Все провайдеры (включая кастомные id); Kimi/OpenRouter/AgyStatus — легаси-зеркала
        public List<ProviderState> Providers { get; set; }

        public QuotaSnapshot() {
            Status = "online";
            LastUpdated = "";
            AgyStatus = "online";
            AgyLastUpdated = "";
            Gemini5h = new BucketInfo();
            GeminiWeekly = new BucketInfo();
            Claude5h = new BucketInfo();
            ClaudeWeekly = new BucketInfo();
            OpenRouter = new OpenRouterInfo();
            Kimi = new KimiInfo();
            Providers = new List<ProviderState>();
        }

        public static BucketInfo ParseBucket(Dictionary<string, object> bucket) {
            var info = new BucketInfo();
            if (bucket == null) return info;

            if (bucket.ContainsKey("used_fraction") && bucket["used_fraction"] != null) {
                info.Fraction = SimpleJson.GetDouble(bucket, "used_fraction", 0.0);
                info.Percent = SimpleJson.GetDouble(bucket, "used_percent", Math.Round(info.Fraction * 100.0, 1));
            } else if (bucket.ContainsKey("fraction") && bucket["fraction"] != null) {
                double remFrac = SimpleJson.GetDouble(bucket, "fraction", 1.0);
                info.Fraction = Math.Max(0.0, Math.Min(1.0, 1.0 - remFrac));
                info.Percent = Math.Round(info.Fraction * 100.0, 1);
            }

            info.ResetIn = SimpleJson.GetString(bucket, "reset_in", "--");
            return info;
        }

        public static QuotaSnapshot FromDictionary(Dictionary<string, object> root, string statusOverride) {
            var snap = new QuotaSnapshot();
            if (root == null) return snap;

            snap.Status = !string.IsNullOrEmpty(statusOverride) ? statusOverride : SimpleJson.GetString(root, "status", "online");
            snap.LastUpdated = SimpleJson.GetString(root, "last_updated", "");

            var data = SimpleJson.GetObject(root, "data") ?? root;

            // Gemini / Claude buckets: first look inside data.agy (new format),
            // fallback to flat data.gemini_5h etc. (old format files on disk).
            var agyBucketsMeta = SimpleJson.GetObject(data, "agy") ?? new Dictionary<string, object>();

            snap.Gemini5h = ParseBucket(SimpleJson.GetObject(agyBucketsMeta, "gemini_5h") ?? SimpleJson.GetObject(data, "gemini_5h"));
            snap.GeminiWeekly = ParseBucket(SimpleJson.GetObject(agyBucketsMeta, "gemini_weekly") ?? SimpleJson.GetObject(data, "gemini_weekly"));

            // Claude
            snap.Claude5h = ParseBucket(SimpleJson.GetObject(agyBucketsMeta, "claude_5h") ?? SimpleJson.GetObject(data, "claude_5h"));
            snap.ClaudeWeekly = ParseBucket(SimpleJson.GetObject(agyBucketsMeta, "claude_weekly") ?? SimpleJson.GetObject(data, "claude_weekly"));

            // OpenRouter
            var orDict = SimpleJson.GetObject(data, "openrouter") ?? SimpleJson.GetObject(root, "openrouter");
            if (orDict != null) {
                var or = snap.OpenRouter;
                or.Status = SimpleJson.GetString(orDict, "status", "online");
                or.LastUpdated = SimpleJson.GetString(orDict, "last_updated", snap.LastUpdated);
                or.Limit = SimpleJson.GetDouble(orDict, "limit", 0.0);
                or.Usage = SimpleJson.GetDouble(orDict, "usage", 0.0);
                or.LimitRemaining = orDict.ContainsKey("limit_remaining")
                    ? SimpleJson.GetDouble(orDict, "limit_remaining", Math.Max(0.0, or.Limit - or.Usage))
                    : Math.Max(0.0, or.Limit - or.Usage);

                if (orDict.ContainsKey("used_fraction")) {
                    or.UsedFraction = SimpleJson.GetDouble(orDict, "used_fraction", 0.0);
                } else if (or.Limit > 0) {
                    or.UsedFraction = or.Usage / or.Limit;
                }

                or.UsedPercent = orDict.ContainsKey("used_percent")
                    ? SimpleJson.GetDouble(orDict, "used_percent", Math.Round(or.UsedFraction * 100.0, 1))
                    : Math.Round(or.UsedFraction * 100.0, 1);

                or.ResetIn = SimpleJson.GetString(orDict, "reset_in", SimpleJson.GetString(orDict, "limit_reset", "--"));
                or.LimitReset = SimpleJson.GetString(orDict, "limit_reset", "monthly");
                or.FreeDailyLimit = SimpleJson.GetInt(orDict, "free_daily_limit", 0);
                or.FreeDailyUsed = SimpleJson.GetInt(orDict, "free_daily_used", 0);
            }

            // Kimi
            var kimiDict = SimpleJson.GetObject(data, "kimi") ?? SimpleJson.GetObject(root, "kimi");
            if (kimiDict != null) {
                var k = snap.Kimi;
                k.Status = SimpleJson.GetString(kimiDict, "status", "online");
                k.LastUpdated = SimpleJson.GetString(kimiDict, "last_updated", snap.LastUpdated);

                var k5 = SimpleJson.GetObject(kimiDict, "limit_5h");
                if (k5 != null) {
                    double ratio = SimpleJson.GetDouble(k5, "used_ratio", 0.0);
                    double pct = SimpleJson.GetDouble(k5, "used_percent", Math.Round(ratio * 100.0, 1));
                    string reset = SimpleJson.GetString(k5, "reset_in", "--");
                    k.Limit5h = new BucketInfo { Fraction = ratio, Percent = pct, ResetIn = reset };
                }

                var kWk = SimpleJson.GetObject(kimiDict, "limit_7d");
                if (kWk != null) {
                    double ratio = SimpleJson.GetDouble(kWk, "used_ratio", 0.0);
                    double pct = SimpleJson.GetDouble(kWk, "used_percent", Math.Round(ratio * 100.0, 1));
                    string reset = SimpleJson.GetString(kWk, "reset_in", "--");
                    k.Limit7d = new BucketInfo { Fraction = ratio, Percent = pct, ResetIn = reset };
                }
            }

            // AgY card (Gemini + Claude/GPT) — single status/timestamp for the whole block
            var agyMeta = SimpleJson.GetObject(data, "agy");
            if (agyMeta != null) {
                snap.AgyStatus = SimpleJson.GetString(agyMeta, "status", "online");
                snap.AgyLastUpdated = SimpleJson.GetString(agyMeta, "last_updated", snap.LastUpdated);
                snap.Providers.Add(new ProviderState {
                    Id = "agy", Kind = "agy",
                    Status = snap.AgyStatus, LastUpdated = snap.AgyLastUpdated
                });
            } else {
                snap.AgyStatus = snap.Status;
                snap.AgyLastUpdated = snap.LastUpdated;
            }

            // OpenRouter legacy mirror -> ProviderState
            snap.Providers.Add(new ProviderState {
                Id = "openrouter", Kind = "openrouter",
                Status = snap.OpenRouter.Status, LastUpdated = snap.OpenRouter.LastUpdated,
                OpenRouter = snap.OpenRouter
            });
            snap.Providers.Add(new ProviderState {
                Id = "kimi", Kind = "kimi",
                Status = snap.Kimi.Status, LastUpdated = snap.Kimi.LastUpdated,
                Kimi = snap.Kimi
            });

            // Кастомные провайдеры: любые data.<id>, не являющиеся аги/легаси-зеркалами.
            // Формат определяется по ключам блока: limit_5h -> kimi-окна,
            // limit/usage -> openrouter-кредиты.
            var legacy = new System.Collections.Generic.HashSet<string>(StringComparer.OrdinalIgnoreCase);
            legacy.Add("agy"); legacy.Add("openrouter"); legacy.Add("kimi");
            foreach (var kv in data) {
                if (legacy.Contains(kv.Key)) continue;
                var block = kv.Value as Dictionary<string, object>;
                if (block == null) continue;

                var ps = new ProviderState { Id = kv.Key };
                ps.Status = SimpleJson.GetString(block, "status", "online");
                ps.LastUpdated = SimpleJson.GetString(block, "last_updated", snap.LastUpdated);

                if (block.ContainsKey("limit_5h")) {
                    ps.Kind = "kimi";
                    var k = new KimiInfo { Status = ps.Status, LastUpdated = ps.LastUpdated };
                    var k5 = SimpleJson.GetObject(block, "limit_5h");
                    if (k5 != null) {
                        double ratio = SimpleJson.GetDouble(k5, "used_ratio", 0.0);
                        double pct = SimpleJson.GetDouble(k5, "used_percent", Math.Round(ratio * 100.0, 1));
                        k.Limit5h = new BucketInfo { Fraction = ratio, Percent = pct, ResetIn = SimpleJson.GetString(k5, "reset_in", "--") };
                    }
                    var k7 = SimpleJson.GetObject(block, "limit_7d");
                    if (k7 != null) {
                        double ratio = SimpleJson.GetDouble(k7, "used_ratio", 0.0);
                        double pct = SimpleJson.GetDouble(k7, "used_percent", Math.Round(ratio * 100.0, 1));
                        k.Limit7d = new BucketInfo { Fraction = ratio, Percent = pct, ResetIn = SimpleJson.GetString(k7, "reset_in", "--") };
                    }
                    ps.Kimi = k;
                } else {
                    ps.Kind = "openrouter";
                    var or = new OpenRouterInfo { Status = ps.Status, LastUpdated = ps.LastUpdated };
                    or.Limit = SimpleJson.GetDouble(block, "limit", 0.0);
                    or.Usage = SimpleJson.GetDouble(block, "usage", 0.0);
                    or.LimitRemaining = block.ContainsKey("limit_remaining")
                        ? SimpleJson.GetDouble(block, "limit_remaining", Math.Max(0.0, or.Limit - or.Usage))
                        : Math.Max(0.0, or.Limit - or.Usage);
                    or.UsedFraction = SimpleJson.GetDouble(block, "used_fraction", or.Limit > 0 ? or.Usage / or.Limit : 0.0);
                    or.UsedPercent = SimpleJson.GetDouble(block, "used_percent", Math.Round(or.UsedFraction * 100.0, 1));
                    or.ResetIn = SimpleJson.GetString(block, "reset_in", "--");
                    or.LimitReset = SimpleJson.GetString(block, "limit_reset", "monthly");
                    or.FreeDailyLimit = SimpleJson.GetInt(block, "free_daily_limit", 0);
                    or.FreeDailyUsed = SimpleJson.GetInt(block, "free_daily_used", 0);
                    ps.OpenRouter = or;
                }
                snap.Providers.Add(ps);
            }

            return snap;
        }
    }
}
