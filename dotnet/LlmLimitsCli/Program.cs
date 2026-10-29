using System;
using System.Collections.Generic;
using System.IO;
using System.Globalization;
using System.Text;
using System.Threading;
using LlmLimitsOverlay;

namespace LlmLimitsCli {
    class CliOptions {
        public bool Json = false;
        public string Provider = null;
        public int WatchSeconds = 0;
        public bool NoCache = false;
        public string EnvPath = null;
        public string ConfigPath = null;
        public string AgyBin = null;
        public int AgyTimeoutSeconds = 0; // 0 = не задан (env/default)
        public bool NoColor = false;
        public bool Help = false;
    }

    class Program {
        static bool _colorEnabled = true;
        static volatile bool _cancelRequested = false;

        static int Main(string[] args) {
            Console.OutputEncoding = Encoding.UTF8;

            CliOptions opt;
            string parseError;
            if (!TryParseArgs(args, out opt, out parseError)) {
                Console.Error.WriteLine("llm-limits: " + parseError);
                Console.Error.WriteLine("Try 'llm-limits --help' for usage.");
                return 1;
            }

            if (opt.Help) {
                PrintHelp();
                return 0;
            }

            _colorEnabled = !opt.NoColor
                && Environment.GetEnvironmentVariable("NO_COLOR") == null
                && !Console.IsOutputRedirected;

            string appDir = AppContext.BaseDirectory;
            if (string.IsNullOrEmpty(appDir)) appDir = Environment.CurrentDirectory;

            // Initialize config
            Config.Initialize(appDir);
            Config.InitializeWithFile(opt.ConfigPath);
            Config config = Config.Load();

            // Apply CLI overrides
            if (opt.NoCache) {
                QuotaFetcher.BypassAgyCache = true;
            }
            if (!string.IsNullOrEmpty(opt.AgyBin)) {
                Environment.SetEnvironmentVariable("LLM_LIMITS_AGY_BIN", opt.AgyBin);
            }

            // Logger: file only (CLI stdout/stderr must stay clean)
            Logger.Initialize(appDir);

            if (opt.WatchSeconds > 0) {
                return RunWatch(opt, appDir, config);
            }

            QuotaSnapshot snap = FetchOnce(appDir, opt);
            if (snap == null) {
                Console.Error.WriteLine("llm-limits: fetch failed and no cache available.");
                return 1;
            }

            Render(opt, snap);
            return ExitCodeFor(opt, snap);
        }

        // ------------------------------------------------------------------
        // Argument parsing
        // ------------------------------------------------------------------
        static bool TryParseArgs(string[] args, out CliOptions opt, out string error) {
            opt = new CliOptions();
            error = null;

            for (int i = 0; i < args.Length; i++) {
                string a = args[i];
                switch (a) {
                    case "--json":
                        opt.Json = true;
                        break;
                    case "--provider":
                        if (i + 1 >= args.Length) { error = "--provider requires a value (kimi|openrouter|agy)."; return false; }
                        opt.Provider = args[++i].Trim().ToLowerInvariant();
                        if (opt.Provider != "kimi" && opt.Provider != "openrouter" && opt.Provider != "agy") {
                            error = "unknown provider '" + opt.Provider + "' (expected kimi|openrouter|agy).";
                            return false;
                        }
                        break;
                    case "--watch":
                        if (i + 1 >= args.Length) { error = "--watch requires seconds value."; return false; }
                        int w;
                        if (!int.TryParse(args[++i], NumberStyles.Integer, CultureInfo.InvariantCulture, out w) || w < 2) {
                            error = "--watch value must be an integer >= 2.";
                            return false;
                        }
                        opt.WatchSeconds = w;
                        break;
                    case "--no-cache":
                        opt.NoCache = true;
                        break;
                    case "--env":
                        if (i + 1 >= args.Length) { error = "--env requires a path."; return false; }
                        opt.EnvPath = args[++i];
                        break;
                    case "--config":
                        if (i + 1 >= args.Length) { error = "--config requires a path."; return false; }
                        opt.ConfigPath = args[++i];
                        break;
                    case "--agy-bin":
                        if (i + 1 >= args.Length) { error = "--agy-bin requires a path."; return false; }
                        opt.AgyBin = args[++i];
                        break;
                    case "--agy-timeout":
                        if (i + 1 >= args.Length) { error = "--agy-timeout requires seconds (1..120)."; return false; }
                        int t;
                        if (!int.TryParse(args[++i], NumberStyles.Integer, CultureInfo.InvariantCulture, out t) || t < 1 || t > 120) {
                            error = "--agy-timeout value must be an integer 1..120.";
                            return false;
                        }
                        opt.AgyTimeoutSeconds = t;
                        break;
                    case "--no-color":
                        opt.NoColor = true;
                        break;
                    case "--help":
                    case "-h":
                    case "/?":
                        opt.Help = true;
                        break;
                    default:
                        error = "unknown argument '" + a + "'.";
                        return false;
                }
            }
            return true;
        }

        static void PrintHelp() {
            Console.WriteLine("llm-limits - LLM quota status in the terminal (Kimi / OpenRouter / Gemini+Claude-GPT)");
            Console.WriteLine();
            Console.WriteLine("Usage: llm-limits [options]");
            Console.WriteLine();
            Console.WriteLine("Options:");
            Console.WriteLine("  (no args)               Human-readable table of all providers");
            Console.WriteLine("  --json                  Machine-readable JSON output");
            Console.WriteLine("  --provider <name>       Only kimi | openrouter | agy");
            Console.WriteLine("  --watch <seconds>       Refresh loop (default 60s interval is used when omitted)");
            Console.WriteLine("  --no-cache              Bypass the agy /usage cache (forces agy.exe run)");
            Console.WriteLine("  --env <path>            Use explicit .env file");
            Console.WriteLine("  --config <path>         Use explicit overlay.config.json");
            Console.WriteLine("  --agy-bin <path>        Explicit path to agy.exe");
            Console.WriteLine("  --agy-timeout <sec>     agy spawn timeout, 1..120 (default: Windows 8s, Linux/macOS 20s; env LLM_LIMITS_AGY_TIMEOUT)");
            Console.WriteLine("  --no-color              Disable ANSI colors (also honors NO_COLOR)");
            Console.WriteLine("  --help                  Show this help");
            Console.WriteLine();
            Console.WriteLine("Exit codes: 0 = all providers online, 2 = some stale, 1 = all failed / config error");
            Console.WriteLine("API keys and OpenRouter labels are never printed.");
        }

        // ------------------------------------------------------------------
        // Fetching
        // ------------------------------------------------------------------
        static QuotaSnapshot FetchOnce(string appDir, CliOptions opt) {
            var done = new ManualResetEventSlim(false);
            QuotaSnapshot result = null;

            var fetcher = new QuotaFetcher(appDir,
                snap => { result = snap; },
                (fetching, statusText) => { if (!fetching) done.Set(); },
                null);

            var cfg = Config.Load();

            if (!string.IsNullOrEmpty(opt.EnvPath)) {
                fetcher.EnvFileOverride = opt.EnvPath;
            }

            // Таймаут agy: флаг --agy-timeout > env LLM_LIMITS_AGY_TIMEOUT > платформенный дефолт
            if (opt.AgyTimeoutSeconds > 0) {
                fetcher.AgyTimeoutSeconds = opt.AgyTimeoutSeconds;
            } else {
                string envT = Environment.GetEnvironmentVariable("LLM_LIMITS_AGY_TIMEOUT");
                int t;
                if (!string.IsNullOrEmpty(envT)
                    && int.TryParse(envT.Trim(), NumberStyles.Integer, CultureInfo.InvariantCulture, out t)
                    && t >= 1 && t <= 120) {
                    fetcher.AgyTimeoutSeconds = t;
                }
            }

            // Общий бюджет цикла должен покрывать таймаут agy + запас
            if (cfg.TimeoutSeconds < fetcher.AgyTimeoutSeconds + 5) {
                cfg.TimeoutSeconds = fetcher.AgyTimeoutSeconds + 5;
            }

            fetcher.StartAsyncRefresh(cfg);

            // Overall safety timeout: 8s agy + slack
            if (!done.Wait(25000)) {
                return null;
            }
            return result;
        }

        static int RunWatch(CliOptions opt, string appDir, Config config) {
            bool tty = !Console.IsOutputRedirected;
            Console.CancelKeyPress += (s, e) => { e.Cancel = true; _cancelRequested = true; };

            int lastCode = 1;
            bool first = true;
            while (!_cancelRequested) {
                QuotaSnapshot snap = FetchOnce(appDir, opt);
                if (snap == null) {
                    Console.Error.WriteLine("[" + DateTime.Now.ToString("HH:mm:ss") + "] fetch failed");
                    lastCode = 1;
                } else {
                    lastCode = ExitCodeFor(opt, snap);
                    if (opt.Json) {
                        Console.WriteLine(SimpleJson.Serialize(BuildJsonPayload(opt, snap), false));
                    } else {
                        if (tty && !first) {
                            Console.Clear();
                        } else if (!tty && !first) {
                            Console.WriteLine();
                        }
                        Render(opt, snap);
                        if (!tty) {
                            Console.WriteLine("--- tick " + DateTime.Now.ToString("yyyy-MM-dd HH:mm:ss") + " ---");
                        }
                    }
                }
                first = false;

                int waited = 0;
                while (waited < opt.WatchSeconds * 1000 && !_cancelRequested) {
                    Thread.Sleep(250);
                    waited += 250;
                }
            }
            return lastCode;
        }

        // ------------------------------------------------------------------
        // Rendering
        // ------------------------------------------------------------------
        static void Render(CliOptions opt, QuotaSnapshot snap) {
            if (opt.Json) {
                Console.WriteLine(SimpleJson.Serialize(BuildJsonPayload(opt, snap), true));
                return;
            }

            if (opt.Provider == null || opt.Provider == "kimi") {
                RenderProviderHeader("KIMI", snap.Kimi.Status, snap.Kimi.LastUpdated);
                RenderBucket("  7d", snap.Kimi.Limit7d);
                RenderBucket("  5h", snap.Kimi.Limit5h);
            }
            if (opt.Provider == null || opt.Provider == "openrouter") {
                RenderProviderHeader("OPENROUTER", snap.OpenRouter.Status, snap.OpenRouter.LastUpdated);
                var or = snap.OpenRouter;
                string pct = or.Limit > 0 ? or.UsedPercent.ToString("0.#", CultureInfo.InvariantCulture) + "% used"
                                          : or.UsedPercent.ToString("0.#", CultureInfo.InvariantCulture) + "% used (no limit)";
                Console.WriteLine("       " + pct
                    + "  reset " + Safe(or.ResetIn)
                    + "  free " + or.FreeDailyUsed + "/" + or.FreeDailyLimit
                    + "  rem. $" + or.LimitRemaining.ToString("0.00", CultureInfo.InvariantCulture));
            }
            if (opt.Provider == null || opt.Provider == "agy") {
                RenderProviderHeader("GEMINI + CLAUDE/GPT", snap.AgyStatus, snap.AgyLastUpdated);
                RenderBucket("  gemini 7d", snap.GeminiWeekly);
                RenderBucket("  gemini 5h", snap.Gemini5h);
                RenderBucket("  claude 7d", snap.ClaudeWeekly);
                RenderBucket("  claude 5h", snap.Claude5h);
            }
        }

        static void RenderProviderHeader(string name, string status, string lastUpdated) {
            ConsoleColor? color = StatusColor(status);
            WriteColored(name + "  ", color);
            WriteColored(status ?? "unknown", color);
            string age = RelativeAge(lastUpdated);
            if (age.Length > 0) {
                Console.WriteLine("  (" + age + ")");
            } else {
                Console.WriteLine();
            }
        }

        static void RenderBucket(string label, BucketInfo b) {
            if (b == null) return;
            Console.WriteLine("       " + label + "  " + b.Percent.ToString("0.#", CultureInfo.InvariantCulture) + "%"
                + "  reset " + Safe(b.ResetIn));
        }

        static string Safe(string s) {
            return string.IsNullOrEmpty(s) ? "--" : s;
        }

        static ConsoleColor? StatusColor(string status) {
            if (!_colorEnabled) return null;
            switch ((status ?? "").ToLowerInvariant()) {
                case "online": return ConsoleColor.Green;
                case "stale": return ConsoleColor.DarkYellow;
                case "no_key": return ConsoleColor.Gray;
                case "error": return ConsoleColor.Red;
                default: return null;
            }
        }

        static void WriteColored(string text, ConsoleColor? color) {
            if (color.HasValue) {
                var prev = Console.ForegroundColor;
                Console.ForegroundColor = color.Value;
                Console.Write(text);
                Console.ForegroundColor = prev;
            } else {
                Console.Write(text);
            }
        }

        // Same semantics as the GUI relative-time text, without WPF theme deps.
        static string RelativeAge(string updatedStr) {
            if (string.IsNullOrEmpty(updatedStr)) return "";
            try {
                string[] formats = new string[] { "yyyy-MM-dd HH:mm:ss" };
                DateTime dt = DateTime.ParseExact(updatedStr, formats, CultureInfo.InvariantCulture, DateTimeStyles.None);
                int totalMinutes = (int)Math.Floor((DateTime.Now - dt).TotalMinutes);
                if (totalMinutes < 1) return "только что";
                if (totalMinutes < 60) return totalMinutes + " мин";
                int hrs = totalMinutes / 60;
                int mins = totalMinutes % 60;
                return mins > 0 ? (hrs + "ч " + mins + "м") : (hrs + "ч");
            } catch {
                return "";
            }
        }

        // ------------------------------------------------------------------
        // JSON payload (no keys, no raw OpenRouter label)
        // ------------------------------------------------------------------
        static Dictionary<string, object> BuildJsonPayload(CliOptions opt, QuotaSnapshot snap) {
            var providers = new Dictionary<string, object>();

            if (opt.Provider == null || opt.Provider == "kimi") {
                providers["kimi"] = new Dictionary<string, object> {
                    { "status", snap.Kimi.Status },
                    { "last_updated", snap.Kimi.LastUpdated },
                    { "limit_5h", BucketJson(snap.Kimi.Limit5h) },
                    { "limit_7d", BucketJson(snap.Kimi.Limit7d) }
                };
            }
            if (opt.Provider == null || opt.Provider == "openrouter") {
                var or = snap.OpenRouter;
                providers["openrouter"] = new Dictionary<string, object> {
                    { "status", or.Status },
                    { "last_updated", or.LastUpdated },
                    { "limit", or.Limit },
                    { "usage", or.Usage },
                    { "limit_remaining", or.LimitRemaining },
                    { "used_percent", or.UsedPercent },
                    { "used_fraction", or.UsedFraction },
                    { "reset_in", or.ResetIn },
                    { "free_daily_used", or.FreeDailyUsed },
                    { "free_daily_limit", or.FreeDailyLimit }
                    // NOTE: no "label" — never leak the key
                };
            }
            if (opt.Provider == null || opt.Provider == "agy") {
                providers["agy"] = new Dictionary<string, object> {
                    { "status", snap.AgyStatus },
                    { "last_updated", snap.AgyLastUpdated },
                    { "gemini_5h", BucketJson(snap.Gemini5h) },
                    { "gemini_weekly", BucketJson(snap.GeminiWeekly) },
                    { "claude_5h", BucketJson(snap.Claude5h) },
                    { "claude_weekly", BucketJson(snap.ClaudeWeekly) }
                };
            }

            return new Dictionary<string, object> {
                { "status", snap.Status },
                { "last_updated", snap.LastUpdated },
                { "providers", providers }
            };
        }

        static Dictionary<string, object> BucketJson(BucketInfo b) {
            if (b == null) return null;
            return new Dictionary<string, object> {
                { "used_percent", b.Percent },
                { "used_fraction", b.Fraction },
                { "reset_in", b.ResetIn }
            };
        }

        // ------------------------------------------------------------------
        // Exit codes
        // ------------------------------------------------------------------
        static int ExitCodeFor(CliOptions opt, QuotaSnapshot snap) {
            string kimiStatus = (snap.Kimi.Status ?? "").ToLowerInvariant();
            string orStatus = (snap.OpenRouter.Status ?? "").ToLowerInvariant();
            string agyStatus = (snap.AgyStatus ?? "").ToLowerInvariant();
            string rootStatus = (snap.Status ?? "").ToLowerInvariant();

            if (opt.Provider == "kimi") return CodeFor(kimiStatus);
            if (opt.Provider == "openrouter") return CodeFor(orStatus);
            if (opt.Provider == "agy") return CodeFor(agyStatus);

            if (rootStatus == "error") return 1;
            int online = 0;
            int total = 0;
            foreach (string st in new string[] { kimiStatus, orStatus, agyStatus }) {
                total++;
                if (st == "online") online++;
            }
            if (online == total) return 0;
            if (online == 0) return 1;
            return 2;
        }

        static int CodeFor(string status) {
            switch (status) {
                case "online": return 0;
                case "stale": return 2;
                case "no_key": return 2;
                case "disabled": return 2;
                default: return 1;
            }
        }
    }
}