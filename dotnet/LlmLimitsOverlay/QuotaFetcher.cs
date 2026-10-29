using System;
using System.Collections;
using System.Collections.Generic;
using System.Diagnostics;
using System.Globalization;
using System.IO;
using System.Net;
using System.Text;
using System.Threading;
using System.Threading.Tasks;

namespace LlmLimitsOverlay {
    public class AgyWorkerResult {
        public string Status { get; set; }
        public Dictionary<string, object> Buckets { get; set; }
        public object Raw { get; set; }
        public bool CacheHit { get; set; }
        public string Error { get; set; }
        public string LastUpdated { get; set; }
    }

    public class QuotaFetcher {
        private const double HttpTimeoutSeconds = 3.5;
        private const int AgyTimeoutSeconds = 8;
        private const double DefaultAgyCacheTtlSeconds = 300.0;

        private readonly string _appDirectory;
        private readonly string _limitsJsonPath;
        private readonly string _fallbackJsonPath;
        private readonly Action<QuotaSnapshot> _onDataLoaded;
        private readonly Action<bool, string> _onFetchStateChanged;
        // UI-thread marshaler. GUI passes a Dispatcher-based one; console/other
        // hosts use the default (execute inline).
        private readonly Action<Action> _dispatch;
        // When true, agy /usage disk+memory cache is bypassed (CLI --no-cache).
        public static bool BypassAgyCache = false;
        // CLI override: explicit .env file path (CLI --env).
        public string EnvFileOverride = null;

        private static object _agyMemoryCacheRaw = null;
        private static Dictionary<string, object> _agyMemoryCacheData = null;
        private static double _agyMemoryCacheTimestamp = 0;
        private static readonly object _cacheLock = new object();

        private CancellationTokenSource _cts;
        private bool _isFetching = false;

        // Одноразовое логирование плейсхолдер-ключей (чтобы не спамить каждый цикл)
        private static bool _loggedNoKeyOpenRouter = false;
        private static bool _loggedNoKeyKimi = false;

        public bool IsFetching {
            get { return _isFetching; }
        }

        public QuotaFetcher(string appDirectory, Action<QuotaSnapshot> onDataLoaded, Action<bool, string> onFetchStateChanged)
            : this(appDirectory, onDataLoaded, onFetchStateChanged, null) {
        }

        public QuotaFetcher(string appDirectory, Action<QuotaSnapshot> onDataLoaded, Action<bool, string> onFetchStateChanged, Action<Action> dispatch) {
            _appDirectory = appDirectory;
            _limitsJsonPath = Path.Combine(appDirectory, "limits.json");

            string userProfile = Environment.GetFolderPath(Environment.SpecialFolder.UserProfile);
            _fallbackJsonPath = !string.IsNullOrEmpty(userProfile)
                ? Path.Combine(userProfile, @".gemini\antigravity-cli\limits.json")
                : null;

            _onDataLoaded = onDataLoaded;
            _onFetchStateChanged = onFetchStateChanged;
            _dispatch = dispatch ?? (a => a());

            try {
                ServicePointManager.SecurityProtocol |= SecurityProtocolType.Tls12;
            } catch { }
        }

        private void InvokeOnUI(Action action) {
            if (action == null) return;
            if (_dispatch != null) {
                _dispatch(action);
            } else {
                action();
            }
        }

        public QuotaSnapshot LoadCacheFromDisk(string statusOverride = null) {
            bool loaded = false;
            string[] paths = new string[] { _limitsJsonPath, _fallbackJsonPath };

            foreach (string path in paths) {
                if (!string.IsNullOrEmpty(path) && File.Exists(path)) {
                    try {
                        string raw = File.ReadAllText(path, Encoding.UTF8);
                        if (!string.IsNullOrEmpty(raw) && !string.IsNullOrEmpty(raw.Trim())) {
                            var dict = SimpleJson.Deserialize(raw) as Dictionary<string, object>;
                            if (dict != null) {
                                var snap = QuotaSnapshot.FromDictionary(dict, statusOverride);
                                InvokeOnUI(() => {
                                    if (_onDataLoaded != null) _onDataLoaded(snap);
                                });
                                loaded = true;
                                Logger.Log("Cache loaded successfully from " + path + " (status: " + snap.Status + ").");
                                return snap;
                            }
                        }
                    } catch (Exception ex) {
                        Logger.Log("Error loading cache from " + path + ": " + ex.Message);
                    }
                }
            }

            if (!loaded) {
                var snap = new QuotaSnapshot {
                    Status = !string.IsNullOrEmpty(statusOverride) ? statusOverride : "offline"
                };
                InvokeOnUI(() => {
                    if (_onDataLoaded != null) _onDataLoaded(snap);
                });
                Logger.Log("No cache file found; marked stale (" + statusOverride + ").");
                return snap;
            }

            return null;
        }

        public void CancelRunningFetch() {
            if (_cts != null) {
                try {
                    _cts.Cancel();
                } catch { }
                _cts = null;
            }
            _isFetching = false;
            InvokeOnUI(() => {
                if (_onFetchStateChanged != null) _onFetchStateChanged(false, null);
            });
        }

        public async void StartAsyncRefresh(Config config) {
            if (_isFetching) return;
            _isFetching = true;

            InvokeOnUI(() => {
                if (_onFetchStateChanged != null) _onFetchStateChanged(true, "обновление...");
            });

            _cts = new CancellationTokenSource();
            var cancelToken = _cts.Token;

            try {
                string nowIso = DateTime.Now.ToString("yyyy-MM-dd HH:mm:ss");
                string[] limitsPaths = GetTargetLimitsPaths();
                string envPath = !string.IsNullOrEmpty(EnvFileOverride)
                    ? EnvFileOverride
                    : Path.Combine(_appDirectory, ".env");
                var envParser = new EnvParser(envPath);

                Logger.Log(string.Format("Starting async native refresh at {0}...", nowIso));

                var taskAgy = Task.Run(() => FetchWorkerAgy(limitsPaths, nowIso, envParser, cancelToken));
                var taskOr = Task.Run(() => FetchWorkerOpenRouter(limitsPaths, nowIso, envParser, cancelToken));
                var taskKimi = Task.Run(() => FetchWorkerKimi(limitsPaths, nowIso, envParser, cancelToken));

                int budgetMs = (config != null && config.TimeoutSeconds > 0)
                    ? (int)(config.TimeoutSeconds * 1000)
                    : 10000;

                // Use Task (non-generic) array so all 3 different Task<T> types can be combined
                Task[] allTasksArr = new Task[] { taskAgy, taskOr, taskKimi };
                Task allTasks = Task.WhenAll(allTasksArr);
                Task delayTask = Task.Delay(budgetMs);

                await Task.WhenAny(allTasks, delayTask);

                AgyWorkerResult agyRes = null;
                Dictionary<string, object> orRes = null;
                Dictionary<string, object> kimiRes = null;

                if (taskAgy.IsCompleted && !taskAgy.IsFaulted) {
                    agyRes = taskAgy.Result;
                } else {
                    Logger.Log("Agy fetch incomplete or timed out; falling back to cache.");
                    agyRes = FallbackAgy(limitsPaths, nowIso, "timeout after 8s");
                }

                if (taskOr.IsCompleted && !taskOr.IsFaulted) {
                    orRes = taskOr.Result;
                } else {
                    Logger.Log("OpenRouter fetch incomplete or timed out; falling back to cache.");
                    orRes = FallbackOpenRouter(limitsPaths, nowIso, "timeout");
                }

                if (taskKimi.IsCompleted && !taskKimi.IsFaulted) {
                    kimiRes = taskKimi.Result;
                } else {
                    Logger.Log("Kimi fetch incomplete or timed out; falling back to cache.");
                    kimiRes = FallbackKimi(limitsPaths, nowIso, "timeout");
                }

                var mergedPayload = AssembleAndSaveLimits(limitsPaths, nowIso, agyRes, orRes, kimiRes);
                var snap = QuotaSnapshot.FromDictionary(mergedPayload, null);

                InvokeOnUI(() => {
                    if (_onDataLoaded != null) _onDataLoaded(snap);
                });

                Logger.Log(string.Format("Native refresh finished (agy: {0}, openrouter: {1}, kimi: {2} -> root: {3}).",
                    agyRes.Status,
                    SimpleJson.GetString(orRes, "status", "unknown"),
                    SimpleJson.GetString(kimiRes, "status", "unknown"),
                    SimpleJson.GetString(mergedPayload, "status", "unknown")));
            } catch (Exception ex) {
                Logger.Log("Exception during async native refresh: " + ex.Message);
                LoadCacheFromDisk("stale (error)");
            } finally {
                _isFetching = false;
                InvokeOnUI(() => {
                    if (_onFetchStateChanged != null) _onFetchStateChanged(false, null);
                });
            }
        }

        private string[] GetTargetLimitsPaths() {
            var list = new List<string>();
            if (!string.IsNullOrEmpty(_limitsJsonPath)) list.Add(_limitsJsonPath);
            if (!string.IsNullOrEmpty(_fallbackJsonPath) && Directory.Exists(Path.GetDirectoryName(_fallbackJsonPath))) {
                list.Add(_fallbackJsonPath);
            }
            return list.ToArray();
        }

        // ---------------------------------------------------------------------
        // Provider: AGY
        // ---------------------------------------------------------------------
        private AgyWorkerResult FetchWorkerAgy(string[] limitsPaths, string nowIso, EnvParser env, CancellationToken token) {
            try {
                if (token.IsCancellationRequested) return FallbackAgy(limitsPaths, nowIso, "canceled");

                // 1. Check disk / memory cache
                object cachedRaw;
                Dictionary<string, object> cachedData;
                double cacheTimestamp;
                if (GetCachedAgyUsage(out cachedRaw, out cachedData, out cacheTimestamp)) {
                    var parsedCached = ParseQuotaData(cachedData ?? (cachedRaw as Dictionary<string, object>));
                    string cachedIso = UnixTimeToLocalIso(cacheTimestamp);
                    var cachedFrag = new Dictionary<string, object>();
                    cachedFrag["status"] = "online";
                    cachedFrag["last_updated"] = nowIso;
                    cachedFrag["data"] = parsedCached;
                    cachedFrag["raw"] = cachedRaw;
                    foreach (var kv in parsedCached) cachedFrag[kv.Key] = kv.Value;
                    cachedFrag["cache_hit"] = true;

                    SaveProviderFragment("agy", cachedFrag, limitsPaths);
                    return new AgyWorkerResult {
                        Status = "online",
                        Buckets = parsedCached,
                        Raw = cachedRaw,
                        CacheHit = true,
                        Error = null,
                        LastUpdated = !string.IsNullOrEmpty(cachedIso) ? cachedIso : nowIso
                    };
                }

                // 2. Locate agy executable
                string agyBin = AgyResolver.FindAgyBinary();
                if (string.IsNullOrEmpty(agyBin)) {
                    string missingErr = "missing agy (not found in LLM_LIMITS_AGY_BIN, PATH, or AppData)";
                    Logger.Log("[agy] " + missingErr);
                    return FallbackAgy(limitsPaths, nowIso, missingErr);
                }

                // 3. Anti-popup pre-check: ensure token file exists (blocks interactive OAuth browser launch)
                if (!AgyResolver.CheckAgyAuth()) {
                    string authErr = "auth required: agy not logged in (run 'agy auth login')";
                    Logger.Log("[agy] Token file missing or empty; blocking interactive OAuth popup.");
                    return FallbackAgy(limitsPaths, nowIso, authErr);
                }

                // 4. Direct process execution
                var psi = new ProcessStartInfo {
                    FileName = agyBin,
                    Arguments = "-p /usage --output-format json",
                    UseShellExecute = false,
                    CreateNoWindow = true,
                    RedirectStandardOutput = true,
                    RedirectStandardError = true,
                    StandardOutputEncoding = Encoding.UTF8,
                    StandardErrorEncoding = Encoding.UTF8
                };

                Logger.Log("[live] running " + agyBin + " -p /usage --output-format json...");
                string stdout = "";
                string stderr = "";
                int exitCode = -1;

                using (var proc = new Process { StartInfo = psi }) {
                    var outSb = new StringBuilder();
                    var errSb = new StringBuilder();

                    proc.OutputDataReceived += (s, e) => { if (e.Data != null) outSb.AppendLine(e.Data); };
                    proc.ErrorDataReceived += (s, e) => { if (e.Data != null) errSb.AppendLine(e.Data); };

                    proc.Start();
                    proc.BeginOutputReadLine();
                    proc.BeginErrorReadLine();

                    if (!proc.WaitForExit(AgyTimeoutSeconds * 1000)) {
                        try { proc.Kill(); } catch { }
                        Logger.Log(string.Format("[agy] timed out after {0}s executing agy", AgyTimeoutSeconds));
                        return FallbackAgy(limitsPaths, nowIso, "timeout after 8s executing agy");
                    }

                    exitCode = proc.ExitCode;
                    stdout = outSb.ToString();
                    stderr = errSb.ToString();
                }

                if (exitCode != 0) {
                    string combined = (stdout + " " + stderr).ToLowerInvariant();
                    string err = (combined.Contains("auth") || combined.Contains("login") || combined.Contains("unauthorized"))
                        ? "auth required"
                        : string.Format("agy failed with exit code {0}: {1}", exitCode, string.IsNullOrEmpty(stderr.Trim()) ? stdout.Trim() : stderr.Trim());
                    Logger.Log("[agy] " + err);
                    return FallbackAgy(limitsPaths, nowIso, err);
                }

                var parsed = ExtractLastJson(stdout);
                if (parsed == null) {
                    Logger.Log("[agy] parse failed: invalid JSON output from agy");
                    return FallbackAgy(limitsPaths, nowIso, "parse failed: invalid JSON output from agy");
                }

                string statusStr = SimpleJson.GetString(parsed, "status", "").ToUpperInvariant();
                if (statusStr == "ERROR" || statusStr == "FAILED") {
                    string respText = SimpleJson.GetString(parsed, "response", "");
                    string err = respText.ToLowerInvariant().Contains("auth") || respText.ToLowerInvariant().Contains("login")
                        ? "auth required"
                        : "agy error: " + (string.IsNullOrEmpty(respText) ? "unknown" : respText);
                    Logger.Log("[agy] " + err);
                    return FallbackAgy(limitsPaths, nowIso, err);
                }

                var cmd = SimpleJson.GetObject(parsed, "command");
                var cmdData = (cmd != null && SimpleJson.GetObject(cmd, "data") != null)
                    ? SimpleJson.GetObject(cmd, "data")
                    : (SimpleJson.GetObject(parsed, "response") ?? parsed);

                SaveCachedAgyUsage(parsed, cmdData);

                var parsedBuckets = ParseQuotaData(cmdData ?? parsed);
                var frag = new Dictionary<string, object>();
                frag["status"] = "online";
                frag["last_updated"] = nowIso;
                frag["data"] = parsedBuckets;
                frag["raw"] = parsed;
                foreach (var kv in parsedBuckets) frag[kv.Key] = kv.Value;

                SaveProviderFragment("agy", frag, limitsPaths);

                return new AgyWorkerResult {
                    Status = "online",
                    Buckets = parsedBuckets,
                    Raw = parsed,
                    CacheHit = false,
                    Error = null,
                    LastUpdated = nowIso
                };
            } catch (Exception ex) {
                Logger.Log("[agy] Exception in worker: " + ex.Message);
                return FallbackAgy(limitsPaths, nowIso, ex.Message);
            }
        }

        private AgyWorkerResult FallbackAgy(string[] limitsPaths, string nowIso, string errMsg) {
            var prev = LoadCachedProvider("agy", limitsPaths);
            // Last successful agy update time is preserved in the previous root
            // payload (data.agy.last_updated); the fragment top-level timestamp
            // reflects the failed attempt time, so it is only a last resort.
            string prevSuccessTime = FindPrevAgySuccessTime(limitsPaths);
            string agyStatus;
            Dictionary<string, object> buckets;
            object rawPayload;
            string prevLastUpdated = null;

            if (prev != null && prev.ContainsKey("buckets") && prev["buckets"] is Dictionary<string, object>) {
                buckets = prev["buckets"] as Dictionary<string, object>;
                rawPayload = prev.ContainsKey("raw") ? prev["raw"] : null;
                agyStatus = "stale";
                prevLastUpdated = SimpleJson.GetString(prev, "last_updated", null);
            } else {
                buckets = GetDefaultAgyBuckets("unknown");
                rawPayload = null;
                agyStatus = "error";
            }

            var fragment = new Dictionary<string, object>();
            fragment["status"] = agyStatus;
            fragment["last_updated"] = nowIso;
            fragment["data"] = buckets;
            fragment["raw"] = rawPayload;
            foreach (var kv in buckets) fragment[kv.Key] = kv.Value;
            if (!string.IsNullOrEmpty(errMsg)) fragment["error"] = errMsg;

            SaveProviderFragment("agy", fragment, limitsPaths);

            return new AgyWorkerResult {
                Status = agyStatus,
                Buckets = buckets,
                Raw = rawPayload,
                CacheHit = false,
                Error = errMsg,
                LastUpdated = !string.IsNullOrEmpty(prevSuccessTime)
                    ? prevSuccessTime
                    : (!string.IsNullOrEmpty(prevLastUpdated) ? prevLastUpdated : nowIso)
            };
        }

        private static string FindPrevAgySuccessTime(string[] limitsPaths) {
            foreach (string p in limitsPaths) {
                if (File.Exists(p)) {
                    try {
                        string raw = File.ReadAllText(p, Encoding.UTF8);
                        var dict = SimpleJson.Deserialize(raw) as Dictionary<string, object>;
                        if (dict != null) {
                            var d = SimpleJson.GetObject(dict, "data");
                            var agyMeta = d != null ? SimpleJson.GetObject(d, "agy") : null;
                            if (agyMeta != null) {
                                string ts = SimpleJson.GetString(agyMeta, "last_updated", null);
                                if (!string.IsNullOrEmpty(ts)) return ts;
                            }
                        }
                    } catch { }
                }
            }
            return null;
        }

        // ---------------------------------------------------------------------
        // Provider: OpenRouter
        // ---------------------------------------------------------------------
        private Dictionary<string, object> FetchWorkerOpenRouter(string[] limitsPaths, string nowIso, EnvParser env, CancellationToken token) {
            string key = env.Get("OPENROUTER_API_KEY");
            if (string.IsNullOrEmpty(key) || EnvParser.IsPlaceholderKey(key)) {
                if (!_loggedNoKeyOpenRouter) {
                    Logger.Log("[openrouter] ключ не задан (placeholder) — HTTP пропущен");
                    _loggedNoKeyOpenRouter = true;
                }
                var def = CreateDefaultOpenRouter("no_key", "OPENROUTER_API_KEY не задана или является плейсхолдером");
                SaveProviderFragment("openrouter", WrapFragment(def, nowIso), limitsPaths);
                return def;
            }

            try {
                if (token.IsCancellationRequested) return FallbackOpenRouter(limitsPaths, nowIso, "canceled");

                var req = (HttpWebRequest)WebRequest.Create("https://openrouter.ai/api/v1/key");
                req.Method = "GET";
                req.UserAgent = "LLM-Limits-Overlay/1.0";
                req.Headers.Add("Authorization", "Bearer " + key);
                req.Timeout = (int)(HttpTimeoutSeconds * 1000);
                req.ReadWriteTimeout = (int)(HttpTimeoutSeconds * 1000);

                using (var resp = (HttpWebResponse)req.GetResponse())
                using (var stream = resp.GetResponseStream())
                using (var reader = new StreamReader(stream, Encoding.UTF8)) {
                    string body = reader.ReadToEnd();
                    var root = SimpleJson.Deserialize(body) as Dictionary<string, object>;
                    var data = SimpleJson.GetObject(root, "data") ?? new Dictionary<string, object>();

                    double limitNum = 0.0;
                    if (data.ContainsKey("limit") && data["limit"] != null) {
                        limitNum = SimpleJson.GetDouble(data, "limit", 0.0);
                    }

                    double usage = SimpleJson.GetDouble(data, "usage", 0.0);
                    double limitRemaining = SimpleJson.GetDouble(data, "limit_remaining", 0.0);
                    string limitReset = SimpleJson.GetString(data, "limit_reset", "monthly");

                    string resetsAt = "";
                    var budgets = SimpleJson.GetList(data, "budgets");
                    if (budgets != null && budgets.Count > 0) {
                        var b0 = budgets[0] as Dictionary<string, object>;
                        if (b0 != null) resetsAt = SimpleJson.GetString(b0, "resets_at", "");
                    }
                    if (string.IsNullOrEmpty(resetsAt) && data.ContainsKey("effective_budget")) {
                        var eb = SimpleJson.GetObject(data, "effective_budget");
                        if (eb != null) resetsAt = SimpleJson.GetString(eb, "resets_at", "");
                    }

                    string resetIn = !string.IsNullOrEmpty(resetsAt) ? FormatTimeDiff(resetsAt) : "--";

                    double usedFrac = 0.0;
                    double usedPercent = 0.0;
                    if (limitNum > 0) {
                        usedFrac = Math.Max(0.0, Math.Min(1.0, usage / limitNum));
                        usedPercent = Math.Round(usedFrac * 100.0, 1);
                    }

                    string level = GetUsedStatusLevel(usedFrac);

                    var freeDaily = SimpleJson.GetObject(data, "free_model_daily_requests") ?? new Dictionary<string, object>();
                    int freeUsed = SimpleJson.GetInt(freeDaily, "used", 0);
                    int freeLimit = SimpleJson.GetInt(freeDaily, "limit", 1000);

                    var res = new Dictionary<string, object>();
                    res["status"] = "online";
                    res["last_updated"] = nowIso;
                    res["limit"] = limitNum;
                    res["usage"] = Math.Round(usage, 4);
                    res["limit_remaining"] = Math.Round(limitRemaining, 2);
                    res["used_fraction"] = Math.Round(usedFrac, 4);
                    res["used_percent"] = usedPercent;
                    res["limit_reset"] = limitReset;
                    res["resets_at"] = resetsAt;
                    res["reset_in"] = resetIn;
                    res["level"] = level;
                    res["free_daily_used"] = freeUsed;
                    res["free_daily_limit"] = freeLimit;
                    res["label"] = MaskOpenRouterLabel(SimpleJson.GetString(data, "label", ""));

                    SaveProviderFragment("openrouter", WrapFragment(res, nowIso), limitsPaths);
                    return res;
                }
            } catch (Exception ex) {
                Logger.Log("[openrouter] Request error: " + ex.Message);
                return FallbackOpenRouter(limitsPaths, nowIso, ex.Message);
            }
        }

        private Dictionary<string, object> FallbackOpenRouter(string[] limitsPaths, string nowIso, string errMsg) {
            var prev = LoadCachedProvider("openrouter", limitsPaths);
            Dictionary<string, object> res;
            if (prev != null && (SimpleJson.GetString(prev, "status", "") == "online" || SimpleJson.GetString(prev, "status", "") == "stale")) {
                res = new Dictionary<string, object>(prev, StringComparer.OrdinalIgnoreCase);
                res["status"] = "stale";
                if (!string.IsNullOrEmpty(errMsg)) res["error"] = errMsg;
            } else {
                res = CreateDefaultOpenRouter("error", errMsg);
            }

            SaveProviderFragment("openrouter", WrapFragment(res, nowIso), limitsPaths);
            return res;
        }

        // ---------------------------------------------------------------------
        // Provider: Kimi
        // ---------------------------------------------------------------------
        private Dictionary<string, object> FetchWorkerKimi(string[] limitsPaths, string nowIso, EnvParser env, CancellationToken token) {
            string key = env.Get("KIMI_API_KEY");
            if (string.IsNullOrEmpty(key) || EnvParser.IsPlaceholderKey(key)) {
                if (!_loggedNoKeyKimi) {
                    Logger.Log("[kimi] ключ не задан (placeholder) — HTTP пропущен");
                    _loggedNoKeyKimi = true;
                }
                var def = CreateDefaultKimi("no_key", "KIMI_API_KEY не задана или является плейсхолдером");
                SaveProviderFragment("kimi", WrapFragment(def, nowIso), limitsPaths);
                return def;
            }

            try {
                if (token.IsCancellationRequested) return FallbackKimi(limitsPaths, nowIso, "canceled");

                var req = (HttpWebRequest)WebRequest.Create("https://api.kimi.com/coding/v1/usages");
                req.Method = "GET";
                req.UserAgent = "LLM-Limits-Overlay/1.0";
                req.Headers.Add("Authorization", "Bearer " + key);
                req.Timeout = (int)(HttpTimeoutSeconds * 1000);
                req.ReadWriteTimeout = (int)(HttpTimeoutSeconds * 1000);

                using (var resp = (HttpWebResponse)req.GetResponse())
                using (var stream = resp.GetResponseStream())
                using (var reader = new StreamReader(stream, Encoding.UTF8)) {
                    string body = reader.ReadToEnd();
                    var root = SimpleJson.Deserialize(body) as Dictionary<string, object>;
                    var usages = SimpleJson.GetObject(root, "usages") ?? new Dictionary<string, object>();
                    var u5h = SimpleJson.GetObject(usages, "limit_5h") ?? new Dictionary<string, object>();
                    var u7d = SimpleJson.GetObject(usages, "limit_7d") ?? new Dictionary<string, object>();

                    double? ratio5h = u5h.ContainsKey("used_ratio") && u5h["used_ratio"] != null
                        ? (double?)SimpleJson.GetDouble(u5h, "used_ratio", 0.0)
                        : null;
                    string time5h = SimpleJson.GetString(u5h, "reset_time", "");

                    if (ratio5h == null) {
                        var limitsList = SimpleJson.GetList(root, "limits");
                        if (limitsList != null) {
                            foreach (object limObj in limitsList) {
                                var lim = limObj as Dictionary<string, object>;
                                if (lim == null) continue;
                                var w = SimpleJson.GetObject(lim, "window");
                                if (w != null && SimpleJson.GetInt(w, "duration", 0) == 300) {
                                    var det = SimpleJson.GetObject(lim, "detail") ?? new Dictionary<string, object>();
                                    double lVal = SimpleJson.GetDouble(det, "limit", 100.0);
                                    double uVal = SimpleJson.GetDouble(det, "used", 0.0);
                                    ratio5h = lVal > 0 ? (uVal / lVal) : 0.0;
                                    time5h = SimpleJson.GetString(det, "resetTime", "");
                                    break;
                                }
                            }
                        }
                    }
                    if (ratio5h == null) ratio5h = 0.0;

                    double? ratio7d = u7d.ContainsKey("used_ratio") && u7d["used_ratio"] != null
                        ? (double?)SimpleJson.GetDouble(u7d, "used_ratio", 0.0)
                        : null;
                    string time7d = SimpleJson.GetString(u7d, "reset_time", "");

                    if (ratio7d == null) {
                        var usageObj = SimpleJson.GetObject(root, "usage");
                        if (usageObj != null) {
                            double lVal = SimpleJson.GetDouble(usageObj, "limit", 100.0);
                            double uVal = SimpleJson.GetDouble(usageObj, "used", 0.0);
                            ratio7d = lVal > 0 ? (uVal / lVal) : 0.0;
                            time7d = SimpleJson.GetString(usageObj, "resetTime", "");
                        }
                    }
                    if (ratio7d == null) ratio7d = 0.0;

                    double frac5h = Math.Max(0.0, Math.Min(1.0, ratio5h.Value));
                    double pct5h = Math.Round(frac5h * 100.0, 1);
                    string resetIn5h = !string.IsNullOrEmpty(time5h) ? FormatTimeDiff(time5h) : "--";

                    double frac7d = Math.Max(0.0, Math.Min(1.0, ratio7d.Value));
                    double pct7d = Math.Round(frac7d * 100.0, 1);
                    string resetIn7d = !string.IsNullOrEmpty(time7d) ? FormatTimeDiff(time7d) : "--";

                    var res = new Dictionary<string, object>();
                    res["status"] = "online";
                    res["last_updated"] = nowIso;

                    var l5Dict = new Dictionary<string, object>();
                    l5Dict["used_ratio"] = Math.Round(frac5h, 4);
                    l5Dict["used_percent"] = pct5h;
                    l5Dict["reset_in"] = resetIn5h;
                    l5Dict["reset_time"] = time5h;
                    l5Dict["level"] = GetUsedStatusLevel(frac5h);
                    res["limit_5h"] = l5Dict;

                    var l7Dict = new Dictionary<string, object>();
                    l7Dict["used_ratio"] = Math.Round(frac7d, 4);
                    l7Dict["used_percent"] = pct7d;
                    l7Dict["reset_in"] = resetIn7d;
                    l7Dict["reset_time"] = time7d;
                    l7Dict["level"] = GetUsedStatusLevel(frac7d);
                    res["limit_7d"] = l7Dict;

                    SaveProviderFragment("kimi", WrapFragment(res, nowIso), limitsPaths);
                    return res;
                }
            } catch (Exception ex) {
                Logger.Log("[kimi] Request error: " + ex.Message);
                return FallbackKimi(limitsPaths, nowIso, ex.Message);
            }
        }

        private Dictionary<string, object> FallbackKimi(string[] limitsPaths, string nowIso, string errMsg) {
            var prev = LoadCachedProvider("kimi", limitsPaths);
            Dictionary<string, object> res;
            if (prev != null && (SimpleJson.GetString(prev, "status", "") == "online" || SimpleJson.GetString(prev, "status", "") == "stale")) {
                res = new Dictionary<string, object>(prev, StringComparer.OrdinalIgnoreCase);
                res["status"] = "stale";
                if (!string.IsNullOrEmpty(errMsg)) res["error"] = errMsg;
            } else {
                res = CreateDefaultKimi("error", errMsg);
            }

            SaveProviderFragment("kimi", WrapFragment(res, nowIso), limitsPaths);
            return res;
        }

        // ---------------------------------------------------------------------
        // Merging & Serialization
        // ---------------------------------------------------------------------
        private Dictionary<string, object> WrapFragment(Dictionary<string, object> providerData, string nowIso) {
            var frag = new Dictionary<string, object>(StringComparer.OrdinalIgnoreCase);
            frag["status"] = SimpleJson.GetString(providerData, "status", "unknown");
            frag["last_updated"] = nowIso;
            frag["data"] = providerData;
            foreach (var kv in providerData) {
                if (kv.Key != "data" && kv.Key != "last_updated") {
                    frag[kv.Key] = kv.Value;
                }
            }
            return frag;
        }

        private Dictionary<string, object> AssembleAndSaveLimits(string[] limitsPaths, string nowIso, AgyWorkerResult agyRes, Dictionary<string, object> orRes, Dictionary<string, object> kimiRes) {
            string agyStatus = agyRes != null ? agyRes.Status : "unknown";
            string orStatus = orRes != null ? SimpleJson.GetString(orRes, "status", "unknown") : "unknown";
            string kimiStatus = kimiRes != null ? SimpleJson.GetString(kimiRes, "status", "unknown") : "unknown";

            string rootStatus;
            var statuses = new string[] { agyStatus, orStatus, kimiStatus };
            // online, если хотя бы один провайдер online; иначе error
            if (Array.IndexOf(statuses, "online") != -1) {
                rootStatus = "online";
            } else {
                rootStatus = "error";
            }

            var agyBuckets = (agyRes != null && agyRes.Buckets != null)
                ? agyRes.Buckets
                : GetDefaultAgyBuckets("unknown");

            var agyInfo = new Dictionary<string, object>();
            agyInfo["status"] = agyStatus;
            agyInfo["last_updated"] = (agyRes != null && !string.IsNullOrEmpty(agyRes.LastUpdated)) ? agyRes.LastUpdated : nowIso;
            agyInfo["gemini_5h"] = agyBuckets.ContainsKey("gemini_5h") ? agyBuckets["gemini_5h"] : null;
            agyInfo["gemini_weekly"] = agyBuckets.ContainsKey("gemini_weekly") ? agyBuckets["gemini_weekly"] : null;
            agyInfo["claude_5h"] = agyBuckets.ContainsKey("claude_5h") ? agyBuckets["claude_5h"] : null;
            agyInfo["claude_weekly"] = agyBuckets.ContainsKey("claude_weekly") ? agyBuckets["claude_weekly"] : null;

            var mergedData = new Dictionary<string, object>();
            mergedData["agy"] = agyInfo;
            mergedData["openrouter"] = orRes;
            mergedData["kimi"] = kimiRes;

            var payload = new Dictionary<string, object>();
            payload["status"] = rootStatus;
            payload["last_updated"] = nowIso;
            payload["ls_address"] = null;
            payload["data"] = mergedData;
            payload["raw"] = agyRes != null ? agyRes.Raw : null;
            payload["openrouter"] = orRes;
            payload["kimi"] = kimiRes;
            if (agyRes != null && agyRes.CacheHit) {
                payload["cache_hit"] = true;
            }

            foreach (string p in limitsPaths) {
                WriteAtomicJson(p, payload);
            }

            return payload;
        }

        private static string GetFragmentPath(string limitsPath, string provider) {
            string dir = Path.GetDirectoryName(Path.GetFullPath(limitsPath));
            string fname = Path.GetFileName(limitsPath);
            string stem = fname.EndsWith(".json", StringComparison.OrdinalIgnoreCase)
                ? fname.Substring(0, fname.Length - 5)
                : fname;
            return Path.Combine(dir, string.Format("{0}.{1}.json", stem, provider));
        }

        private static void SaveProviderFragment(string provider, Dictionary<string, object> fragment, string[] limitsPaths) {
            foreach (string p in limitsPaths) {
                string fragPath = GetFragmentPath(p, provider);
                WriteAtomicJson(fragPath, fragment);
            }
        }

        private static bool WriteAtomicJson(string targetPath, object dataObj) {
            try {
                string fullPath = Path.GetFullPath(targetPath);
                string dir = Path.GetDirectoryName(fullPath);
                if (!Directory.Exists(dir)) {
                    Directory.CreateDirectory(dir);
                }

                string tmpPath = fullPath + ".tmp";
                string json = SimpleJson.Serialize(dataObj, true);
                File.WriteAllText(tmpPath, json, Encoding.UTF8);

                if (File.Exists(fullPath)) {
                    File.Delete(fullPath);
                }
                File.Move(tmpPath, fullPath);
                return true;
            } catch (Exception ex) {
                Logger.Log(string.Format("WriteAtomicJson error for {0}: {1}", targetPath, ex.Message));
                return false;
            }
        }

        private static Dictionary<string, object> ExtractProviderFromPayload(string provider, Dictionary<string, object> payload) {
            if (payload == null) return null;

            if (provider == "kimi") {
                var d = SimpleJson.GetObject(payload, "data");
                if (d != null && d.ContainsKey("kimi")) return SimpleJson.GetObject(d, "kimi");
                if (d != null && d.ContainsKey("limit_5h")) return d;
                if (payload.ContainsKey("kimi")) return SimpleJson.GetObject(payload, "kimi");
                if (payload.ContainsKey("limit_5h")) return payload;
                return null;
            } else if (provider == "openrouter") {
                var d = SimpleJson.GetObject(payload, "data");
                if (d != null && d.ContainsKey("openrouter")) return SimpleJson.GetObject(d, "openrouter");
                if (d != null && (d.ContainsKey("limit") || d.ContainsKey("usage"))) return d;
                if (payload.ContainsKey("openrouter")) return SimpleJson.GetObject(payload, "openrouter");
                if (payload.ContainsKey("limit") || payload.ContainsKey("usage")) return payload;
                return null;
            } else if (provider == "agy") {
                var d = SimpleJson.GetObject(payload, "data");
                if (d != null && d.ContainsKey("gemini_5h")) {
                    var buckets = new Dictionary<string, object>();
                    foreach (var k in new string[] { "gemini_5h", "gemini_weekly", "claude_5h", "claude_weekly" }) {
                        if (d.ContainsKey(k)) buckets[k] = d[k];
                    }
                    var res = new Dictionary<string, object>();
                    res["buckets"] = buckets;
                    res["raw"] = payload.ContainsKey("raw") ? payload["raw"] : null;
                    res["status"] = SimpleJson.GetString(payload, "status", "online");
                    res["last_updated"] = SimpleJson.GetString(payload, "last_updated", "");
                    return res;
                }
                if (payload.ContainsKey("gemini_5h")) {
                    var buckets = new Dictionary<string, object>();
                    foreach (var k in new string[] { "gemini_5h", "gemini_weekly", "claude_5h", "claude_weekly" }) {
                        if (payload.ContainsKey(k)) buckets[k] = payload[k];
                    }
                    var res = new Dictionary<string, object>();
                    res["buckets"] = buckets;
                    res["raw"] = payload.ContainsKey("raw") ? payload["raw"] : null;
                    res["status"] = SimpleJson.GetString(payload, "status", "online");
                    res["last_updated"] = SimpleJson.GetString(payload, "last_updated", "");
                    return res;
                }
                return null;
            }

            return null;
        }

        private static Dictionary<string, object> LoadCachedProvider(string provider, string[] limitsPaths) {
            foreach (string p in limitsPaths) {
                string fragPath = GetFragmentPath(p, provider);
                if (File.Exists(fragPath)) {
                    try {
                        string raw = File.ReadAllText(fragPath, Encoding.UTF8);
                        var dict = SimpleJson.Deserialize(raw) as Dictionary<string, object>;
                        if (dict != null) {
                            var extracted = ExtractProviderFromPayload(provider, dict);
                            if (extracted != null) return extracted;
                        }
                    } catch { }
                }
            }

            foreach (string p in limitsPaths) {
                if (File.Exists(p)) {
                    try {
                        string raw = File.ReadAllText(p, Encoding.UTF8);
                        var dict = SimpleJson.Deserialize(raw) as Dictionary<string, object>;
                        if (dict != null) {
                            var extracted = ExtractProviderFromPayload(provider, dict);
                            if (extracted != null) return extracted;
                        }
                    } catch { }
                }
            }

            return null;
        }

        // ---------------------------------------------------------------------
        // Agy Cache & Parsing Helpers
        // ---------------------------------------------------------------------
        private static string GetCacheFilePath() {
            return Path.Combine(Path.GetTempPath(), "llm_limits_agy_usage_cache.json");
        }

        private static double GetQuotaCacheSeconds() {
            string val = Environment.GetEnvironmentVariable("LLM_LIMITS_QUOTA_CACHE_SECONDS");
            if (!string.IsNullOrEmpty(val)) {
                double sec;
                if (double.TryParse(val.Trim(), NumberStyles.Float, CultureInfo.InvariantCulture, out sec)) {
                    return Math.Max(0.0, sec);
                }
            }
            return DefaultAgyCacheTtlSeconds;
        }

        private static double GetCurrentUnixTime() {
            return (DateTime.UtcNow - new DateTime(1970, 1, 1, 0, 0, 0, DateTimeKind.Utc)).TotalSeconds;
        }

        private static string UnixTimeToLocalIso(double unixSeconds) {
            try {
                DateTime dt = new DateTime(1970, 1, 1, 0, 0, 0, DateTimeKind.Utc).AddSeconds(unixSeconds).ToLocalTime();
                return dt.ToString("yyyy-MM-dd HH:mm:ss");
            } catch {
                return null;
            }
        }

        private static bool GetCachedAgyUsage(out object rawObj, out Dictionary<string, object> dataObj, out double cacheTimestamp) {
            rawObj = null;
            dataObj = null;
            cacheTimestamp = 0;

            if (BypassAgyCache) {
                return false;
            }

            double ttl = GetQuotaCacheSeconds();
            double now = GetCurrentUnixTime();

            lock (_cacheLock) {
                // 1. In-memory check
                if (_agyMemoryCacheRaw != null && _agyMemoryCacheData != null) {
                    double age = now - _agyMemoryCacheTimestamp;
                    if (age >= 0 && age < ttl) {
                        rawObj = _agyMemoryCacheRaw;
                        dataObj = _agyMemoryCacheData;
                        cacheTimestamp = _agyMemoryCacheTimestamp;
                        Logger.Log(string.Format("[cache] agy /usage cache hit in-memory (age {0:F1}s <= {1:F0}s), agy.exe not respawned", age, ttl));
                        return true;
                    }
                }

                // 2. Disk check
                string cacheFile = GetCacheFilePath();
                if (File.Exists(cacheFile)) {
                    try {
                        string text = File.ReadAllText(cacheFile, Encoding.UTF8);
                        var cached = SimpleJson.Deserialize(text) as Dictionary<string, object>;
                        if (cached != null && cached.ContainsKey("timestamp") && cached.ContainsKey("data")) {
                            double ts = SimpleJson.GetDouble(cached, "timestamp", 0.0);
                            double age = now - ts;
                            if (age >= 0 && age < ttl) {
                                rawObj = cached.ContainsKey("raw") ? cached["raw"] : null;
                                dataObj = SimpleJson.GetObject(cached, "data");
                                cacheTimestamp = ts;
                                _agyMemoryCacheRaw = rawObj;
                                _agyMemoryCacheData = dataObj;
                                _agyMemoryCacheTimestamp = ts;
                                Logger.Log(string.Format("[cache] agy /usage cache hit (age {0:F1}s <= {1:F0}s), agy.exe not respawned", age, ttl));
                                return true;
                            }
                        }
                    } catch { }
                }
            }

            return false;
        }

        private static void SaveCachedAgyUsage(object rawObj, Dictionary<string, object> dataObj) {
            double now = GetCurrentUnixTime();
            lock (_cacheLock) {
                _agyMemoryCacheRaw = rawObj;
                _agyMemoryCacheData = dataObj;
                _agyMemoryCacheTimestamp = now;

                try {
                    var record = new Dictionary<string, object>();
                    record["timestamp"] = now;
                    record["raw"] = rawObj;
                    record["data"] = dataObj;

                    string cacheFile = GetCacheFilePath();
                    string tmpFile = cacheFile + ".tmp";
                    string json = SimpleJson.Serialize(record, true);
                    File.WriteAllText(tmpFile, json, Encoding.UTF8);
                    if (File.Exists(cacheFile)) File.Delete(cacheFile);
                    File.Move(tmpFile, cacheFile);
                } catch { }
            }
        }

        private static Dictionary<string, object> ExtractLastJson(string text) {
            if (string.IsNullOrEmpty(text)) return null;

            string[] lines = text.Split(new char[] { '\r', '\n' }, StringSplitOptions.RemoveEmptyEntries);
            for (int i = lines.Length - 1; i >= 0; i--) {
                string line = lines[i].Trim();
                if (line.StartsWith("{") && line.EndsWith("}")) {
                    try {
                        var dict = SimpleJson.Deserialize(line) as Dictionary<string, object>;
                        if (dict != null) return dict;
                    } catch { }
                }
            }

            int start = text.LastIndexOf('{');
            int end = text.LastIndexOf('}');
            if (start != -1 && end != -1 && end > start) {
                try {
                    string sub = text.Substring(start, end - start + 1);
                    var dict = SimpleJson.Deserialize(sub) as Dictionary<string, object>;
                    if (dict != null) return dict;
                } catch { }
            }

            return null;
        }

        private static Dictionary<string, object> ParseQuotaData(Dictionary<string, object> resp) {
            var result = new Dictionary<string, object>();
            result["gemini_5h"] = CreateDefaultBucket("ok");
            result["gemini_weekly"] = CreateDefaultBucket("ok");
            result["claude_5h"] = CreateDefaultBucket("ok");
            result["claude_weekly"] = CreateDefaultBucket("ok");

            if (resp == null) return result;

            Dictionary<string, object> dataObj = resp;
            var cmd = SimpleJson.GetObject(resp, "command");
            if (cmd != null && SimpleJson.GetObject(cmd, "data") != null) {
                dataObj = SimpleJson.GetObject(cmd, "data");
            } else if (SimpleJson.GetObject(resp, "response") != null) {
                dataObj = SimpleJson.GetObject(resp, "response");
            }

            var groups = SimpleJson.GetList(dataObj, "groups");
            if (groups == null) return result;

            foreach (object gObj in groups) {
                var group = gObj as Dictionary<string, object>;
                if (group == null) continue;

                string gname = (SimpleJson.GetString(group, "name", "") + " " + SimpleJson.GetString(group, "displayName", "")).ToLowerInvariant();
                var buckets = SimpleJson.GetList(group, "buckets");
                if (buckets == null) continue;

                foreach (object bObj in buckets) {
                    var b = bObj as Dictionary<string, object>;
                    if (b == null) continue;

                    string bid = (SimpleJson.GetString(b, "id", "") + " " + SimpleJson.GetString(b, "bucketId", "")).ToLowerInvariant();
                    string window = SimpleJson.GetString(b, "window", "").ToLowerInvariant();

                    double remFrac = 1.0;
                    if (b.ContainsKey("remaining_fraction") && b["remaining_fraction"] != null) {
                        remFrac = SimpleJson.GetDouble(b, "remaining_fraction", 1.0);
                    } else if (b.ContainsKey("remainingFraction") && b["remainingFraction"] != null) {
                        remFrac = SimpleJson.GetDouble(b, "remainingFraction", 1.0);
                    }

                    string resetTime = SimpleJson.GetString(b, "reset_time", SimpleJson.GetString(b, "resetTime", ""));
                    double pct = Math.Round(remFrac * 100.0, 1);
                    double usedFrac = Math.Max(0.0, Math.Min(1.0, Math.Round(1.0 - remFrac, 4)));
                    double usedPct = Math.Round(usedFrac * 100.0, 1);
                    string level = GetUsedStatusLevel(usedFrac);
                    string resetIn = FormatTimeDiff(resetTime);

                    var info = new Dictionary<string, object>();
                    info["percent"] = pct;
                    info["fraction"] = remFrac;
                    info["used_percent"] = usedPct;
                    info["used_fraction"] = usedFrac;
                    info["reset_in"] = resetIn;
                    info["reset_time"] = resetTime;
                    info["level"] = level;
                    info["displayName"] = SimpleJson.GetString(b, "name", SimpleJson.GetString(b, "displayName", bid));
                    info["description"] = SimpleJson.GetString(b, "description", "");

                    if (gname.Contains("gemini")) {
                        if (window == "5h" || bid.Contains("5h")) {
                            result["gemini_5h"] = info;
                        } else if (window == "weekly" || bid.Contains("weekly")) {
                            result["gemini_weekly"] = info;
                        }
                    } else if (gname.Contains("claude") || gname.Contains("gpt") || gname.Contains("3p")) {
                        if (window == "5h" || bid.Contains("5h")) {
                            result["claude_5h"] = info;
                        } else if (window == "weekly" || bid.Contains("weekly")) {
                            result["claude_weekly"] = info;
                        }
                    } else {
                        if (bid.Contains("gemini")) {
                            if (window == "5h" || bid.Contains("5h")) {
                                result["gemini_5h"] = info;
                            } else if (window == "weekly" || bid.Contains("weekly")) {
                                result["gemini_weekly"] = info;
                            }
                        } else if (bid.Contains("3p") || bid.Contains("claude") || bid.Contains("gpt")) {
                            if (window == "5h" || bid.Contains("5h")) {
                                result["claude_5h"] = info;
                            } else if (window == "weekly" || bid.Contains("weekly")) {
                                result["claude_weekly"] = info;
                            }
                        }
                    }
                }
            }

            return result;
        }

        // ---------------------------------------------------------------------
        // Format & Default Helpers
        // ---------------------------------------------------------------------
        private static bool ContainsCyrillic(string s) {
            if (string.IsNullOrEmpty(s)) return false;
            foreach (char c in s) {
                if (c >= '\u0400' && c <= '\u04FF') return true;
            }
            return false;
        }

        // OpenRouter returns the key itself in data.label when the key has no
        // human-readable name. Never persist or log a raw key: mask anything
        // that looks like one (sk- prefix, or >24 chars without spaces/letters
        // of a natural label), keep friendly names ("My key") untouched.
        private static string MaskOpenRouterLabel(string label) {
            if (string.IsNullOrEmpty(label)) return "";
            string t = label.Trim();
            bool looksLikeKey = t.StartsWith("sk-", StringComparison.OrdinalIgnoreCase)
                || (t.Length > 24 && t.IndexOf(' ') == -1 && !ContainsCyrillic(t));
            if (!looksLikeKey) return label;
            if (t.Length <= 10) return "unnamed";
            return t.Substring(0, 6) + "..." + t.Substring(t.Length - 4);
        }

        public static string FormatTimeDiff(string targetIso) {
            if (string.IsNullOrEmpty(targetIso)) return "N/A";
            DateTime targetUtc;
            if (!DateTime.TryParse(targetIso, CultureInfo.InvariantCulture, DateTimeStyles.AdjustToUniversal | DateTimeStyles.AssumeUniversal, out targetUtc)) {
                return targetIso;
            }

            TimeSpan diff = targetUtc - DateTime.UtcNow;
            int totalSeconds = (int)diff.TotalSeconds;
            if (totalSeconds <= 0) return "ready";

            int days = totalSeconds / 86400;
            int hours = (totalSeconds % 86400) / 3600;
            int minutes = (totalSeconds % 3600) / 60;

            if (days > 0) return string.Format("{0}d {1}h", days, hours);
            if (hours > 0) return string.Format("{0}h {1}m", hours, minutes);
            return string.Format("{0}m", minutes);
        }

        public static string GetUsedStatusLevel(double usedFraction) {
            if (usedFraction >= 0.50) return "critical";
            if (usedFraction >= 0.20) return "warning";
            return "ok";
        }

        private static Dictionary<string, object> CreateDefaultBucket(string level) {
            var b = new Dictionary<string, object>();
            b["percent"] = 100.0;
            b["fraction"] = 1.0;
            b["used_percent"] = 0.0;
            b["used_fraction"] = 0.0;
            b["reset_in"] = "N/A";
            b["reset_time"] = "";
            b["level"] = level;
            return b;
        }

        private static Dictionary<string, object> GetDefaultAgyBuckets(string level) {
            var res = new Dictionary<string, object>();
            res["gemini_5h"] = CreateDefaultBucket(level);
            res["gemini_weekly"] = CreateDefaultBucket(level);
            res["claude_5h"] = CreateDefaultBucket(level);
            res["claude_weekly"] = CreateDefaultBucket(level);
            return res;
        }

        private static Dictionary<string, object> CreateDefaultOpenRouter(string status, string error) {
            var d = new Dictionary<string, object>();
            d["status"] = status;
            d["last_updated"] = "";
            d["error"] = error ?? "OpenRouter not configured";
            d["limit"] = 0.0;
            d["usage"] = 0.0;
            d["limit_remaining"] = 0.0;
            d["used_percent"] = 0.0;
            d["used_fraction"] = 0.0;
            d["reset_in"] = "--";
            d["limit_reset"] = "monthly";
            d["resets_at"] = "";
            d["level"] = "unknown";
            d["free_daily_used"] = 0;
            d["free_daily_limit"] = 1000;
            return d;
        }

        private static Dictionary<string, object> CreateDefaultKimi(string status, string error) {
            var d = new Dictionary<string, object>();
            d["status"] = status;
            d["last_updated"] = "";
            d["error"] = error ?? "Kimi not configured";

            var l5h = new Dictionary<string, object>();
            l5h["used_ratio"] = 0.0;
            l5h["used_percent"] = 0.0;
            l5h["reset_in"] = "--";
            l5h["reset_time"] = "";
            l5h["level"] = "unknown";
            d["limit_5h"] = l5h;

            var l7d = new Dictionary<string, object>();
            l7d["used_ratio"] = 0.0;
            l7d["used_percent"] = 0.0;
            l7d["reset_in"] = "--";
            l7d["reset_time"] = "";
            l7d["level"] = "unknown";
            d["limit_7d"] = l7d;

            return d;
        }
    }
}
