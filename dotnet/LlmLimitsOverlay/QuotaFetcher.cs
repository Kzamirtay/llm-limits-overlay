using System;
using System.Diagnostics;
using System.IO;
using System.Text;
using System.Windows.Threading;

namespace LlmLimitsOverlay {
    public class QuotaFetcher {
        private readonly string _appDirectory;
        private readonly string _limitsJsonPath;
        private readonly string _fallbackJsonPath;
        private readonly Action<QuotaSnapshot> _onDataLoaded;
        private readonly Action<bool, string> _onFetchStateChanged;

        private Process _fetchProc;
        private Stopwatch _fetchWatch;
        private int _currentTimeoutMs = 6000;
        private DispatcherTimer _watchdogTimer;
        private bool _isFetching = false;

        public bool IsFetching {
            get { return _isFetching; }
        }

        public QuotaFetcher(string appDirectory, Action<QuotaSnapshot> onDataLoaded, Action<bool, string> onFetchStateChanged) {
            _appDirectory = appDirectory;
            _limitsJsonPath = Path.Combine(appDirectory, "limits.json");

            string userProfile = Environment.GetFolderPath(Environment.SpecialFolder.UserProfile);
            _fallbackJsonPath = !string.IsNullOrEmpty(userProfile)
                ? Path.Combine(userProfile, @".gemini\antigravity-cli\limits.json")
                : null;

            _onDataLoaded = onDataLoaded;
            _onFetchStateChanged = onFetchStateChanged;

            _watchdogTimer = new DispatcherTimer {
                Interval = TimeSpan.FromMilliseconds(150)
            };
            _watchdogTimer.Tick += OnWatchdogTick;
        }

        public QuotaSnapshot LoadCacheFromDisk(string statusOverride = null) {
            bool loaded = false;
            string[] paths = new string[] { _limitsJsonPath, _fallbackJsonPath };

            foreach (string path in paths) {
                if (!string.IsNullOrEmpty(path) && File.Exists(path)) {
                    try {
                        string raw = File.ReadAllText(path, Encoding.UTF8);
                        if (!string.IsNullOrEmpty(raw) && !string.IsNullOrEmpty(raw.Trim())) {
                            var dict = SimpleJson.Deserialize(raw) as System.Collections.Generic.Dictionary<string, object>;
                            if (dict != null) {
                                var snap = QuotaSnapshot.FromDictionary(dict, statusOverride);
                                if (_onDataLoaded != null) _onDataLoaded(snap);
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
                if (_onDataLoaded != null) _onDataLoaded(snap);
                Logger.Log("No cache file found; marked stale (" + statusOverride + ").");
                return snap;
            }

            return null;
        }

        public void CancelRunningFetch() {
            if (_fetchProc != null && !_fetchProc.HasExited) {
                try {
                    _watchdogTimer.Stop();
                    _fetchProc.Kill();
                    _fetchProc.Dispose();
                } catch { }
                _fetchProc = null;
            }
            _isFetching = false;
            if (_onFetchStateChanged != null) _onFetchStateChanged(false, null);
        }

        public void StartAsyncRefresh(Config config) {
            if (_isFetching) return;

            var backendInfo = BackendResolver.ResolveBackend(config);
            string backendMode = backendInfo.Backend;
            Logger.Log(string.Format("Resolved backend: '{0}' (source: {1})", backendMode, backendInfo.Source));

            double cfgTimeoutSec = (config != null && config.TimeoutSeconds > 0) ? config.TimeoutSeconds : 0.0;
            bool targetIsWsl = false;
            PythonCandidate pyCandidate = null;
            string wslDistro = null;

            if (backendMode == "windows") {
                pyCandidate = BackendResolver.ResolveNativePython(config);
                if (pyCandidate == null) {
                    Logger.Log("Native Python interpreter unavailable for backend 'windows'.");
                    if (_onFetchStateChanged != null) _onFetchStateChanged(false, "нет python");
                    LoadCacheFromDisk("error (нет python)");
                    return;
                }
                targetIsWsl = false;
            } else if (backendMode == "wsl") {
                wslDistro = BackendResolver.ResolveWslDistro(config);
                if (string.IsNullOrEmpty(wslDistro)) {
                    Logger.Log("WSL distro with python3 unavailable for backend 'wsl'.");
                    if (_onFetchStateChanged != null) _onFetchStateChanged(false, "нет wsl");
                    LoadCacheFromDisk("error (нет wsl)");
                    return;
                }
                targetIsWsl = true;
            } else {
                // auto: native python first, then wsl
                pyCandidate = BackendResolver.ResolveNativePython(config);
                if (pyCandidate != null) {
                    targetIsWsl = false;
                } else {
                    Logger.Log("Auto backend: native Python not found, probing WSL...");
                    wslDistro = BackendResolver.ResolveWslDistro(config);
                    if (!string.IsNullOrEmpty(wslDistro)) {
                        targetIsWsl = true;
                    } else {
                        Logger.Log("Auto backend: neither native Python nor WSL available.");
                        if (_onFetchStateChanged != null) _onFetchStateChanged(false, "нет python");
                        LoadCacheFromDisk("error (нет python)");
                        return;
                    }
                }
            }

            if (cfgTimeoutSec > 0) {
                _currentTimeoutMs = (int)(cfgTimeoutSec * 1000);
            } else {
                _currentTimeoutMs = targetIsWsl ? 30000 : 6000;
            }
            double timeoutSec = Math.Round(_currentTimeoutMs / 1000.0, 1);

            _isFetching = true;
            if (_onFetchStateChanged != null) _onFetchStateChanged(true, "обновление...");

            try {
                var psi = new ProcessStartInfo {
                    UseShellExecute = false,
                    CreateNoWindow = true,
                    RedirectStandardOutput = false,
                    RedirectStandardError = false
                };

                if (targetIsWsl) {
                    Logger.Log(string.Format("Starting async fetch via wsl.exe ({0}) [timeout {1}s]...", wslDistro, timeoutSec));
                    string wslFetchScript = BackendResolver.ResolveWslFetchScript(wslDistro, _appDirectory);
                    string wslLimitsArg = "";

                    try {
                        string p = _limitsJsonPath.Replace('\\', '/');
                        var convPsi = new ProcessStartInfo {
                            FileName = "wsl.exe",
                            Arguments = "-d " + wslDistro + " wslpath -u \"" + p + "\"",
                            UseShellExecute = false,
                            CreateNoWindow = true,
                            RedirectStandardOutput = true,
                            RedirectStandardError = true
                        };
                        using (var convProc = Process.Start(convPsi)) {
                            string w = convProc.StandardOutput.ReadToEnd().Trim();
                            if (convProc.WaitForExit(3000) && convProc.ExitCode == 0 && !string.IsNullOrEmpty(w)) {
                                wslLimitsArg = w;
                            }
                        }
                    } catch { }

                    psi.FileName = "wsl.exe";
                    psi.Arguments = !string.IsNullOrEmpty(wslLimitsArg)
                        ? "-d " + wslDistro + " python3 \"" + wslFetchScript + "\" \"" + wslLimitsArg + "\""
                        : "-d " + wslDistro + " python3 \"" + wslFetchScript + "\"";
                } else {
                    string pyScript = Path.Combine(_appDirectory, "fetch_quota.py");
                    Logger.Log(string.Format("Starting async fetch via native Python ({0}) [timeout {1}s]...", pyCandidate.Executable, timeoutSec));
                    psi.FileName = pyCandidate.Executable;
                    psi.Arguments = "\"" + pyScript + "\" \"" + _limitsJsonPath + "\"";
                }

                _fetchProc = Process.Start(psi);
                int procId = _fetchProc.Id;
                Logger.Log(string.Format("Spawned refresh process (PID={0}, cmd={1} {2})", procId, psi.FileName, psi.Arguments));

                _fetchWatch = Stopwatch.StartNew();
                _watchdogTimer.Start();
            } catch (Exception ex) {
                _isFetching = false;
                if (_onFetchStateChanged != null) _onFetchStateChanged(false, targetIsWsl ? "нет wsl" : "нет python");
                Logger.Log("Exception starting refresh process: " + ex.Message);
                LoadCacheFromDisk("error (" + (targetIsWsl ? "WSL unavailable" : "Python unavailable") + ")");
            }
        }

        private void OnWatchdogTick(object sender, EventArgs e) {
            if (_fetchProc == null) {
                _watchdogTimer.Stop();
                _isFetching = false;
                if (_onFetchStateChanged != null) _onFetchStateChanged(false, null);
                return;
            }

            // Case 1: Process finished normally
            if (_fetchProc.HasExited) {
                _watchdogTimer.Stop();
                int exitCode = _fetchProc.ExitCode;
                int procId = 0;
                try { procId = _fetchProc.Id; } catch { }
                try { _fetchProc.Dispose(); } catch { }
                _fetchProc = null;
                _isFetching = false;
                if (_onFetchStateChanged != null) _onFetchStateChanged(false, null);

                Logger.Log(string.Format("Async refresh process finished (PID={0}, exitCode={1}).", procId, exitCode));
                if (exitCode == 0) {
                    LoadCacheFromDisk(null);
                } else {
                    LoadCacheFromDisk("stale (exit " + exitCode + ")");
                }
                return;
            }

            // Case 2: Timed out -> kill
            if (_fetchWatch != null && _fetchWatch.ElapsedMilliseconds > _currentTimeoutMs) {
                _watchdogTimer.Stop();
                int procId = 0;
                try { procId = _fetchProc.Id; } catch { }
                try {
                    _fetchProc.Kill();
                    _fetchProc.Dispose();
                } catch { }
                _fetchProc = null;
                _isFetching = false;
                if (_onFetchStateChanged != null) _onFetchStateChanged(false, null);

                Logger.Log(string.Format("Async refresh timed out after {0}ms (PID={1}). Process killed.", _currentTimeoutMs, procId));
                LoadCacheFromDisk("stale (timeout)");
            }
        }
    }
}
