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

            // Gemini
            snap.Gemini5h = ParseBucket(SimpleJson.GetObject(data, "gemini_5h"));
            snap.GeminiWeekly = ParseBucket(SimpleJson.GetObject(data, "gemini_weekly"));

            // Claude
            snap.Claude5h = ParseBucket(SimpleJson.GetObject(data, "claude_5h"));
            snap.ClaudeWeekly = ParseBucket(SimpleJson.GetObject(data, "claude_weekly"));

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
            } else {
                snap.AgyStatus = snap.Status;
                snap.AgyLastUpdated = snap.LastUpdated;
            }

            return snap;
        }
    }
}
