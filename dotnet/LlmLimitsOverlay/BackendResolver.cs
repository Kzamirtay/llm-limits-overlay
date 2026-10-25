using System;
using System.Collections.Generic;
using System.Diagnostics;
using System.IO;
using System.Text.RegularExpressions;

namespace LlmLimitsOverlay {
    public class PythonCandidate {
        public string Command { get; set; }
        public string PrefixArgs { get; set; }
        public string Executable { get; set; }
    }

    public class BackendResolutionResult {
        public string Backend { get; set; }
        public string Source { get; set; }
    }

    public static class BackendResolver {
        private static PythonCandidate _cachedNativePython = null;
        private static string _cachedWslDistro = null;
        private static readonly object _syncLock = new object();

        public static void ClearCache() {
            lock (_syncLock) {
                _cachedNativePython = null;
                _cachedWslDistro = null;
            }
        }

        public static BackendResolutionResult ResolveBackend(Config config) {
            string envB = Environment.GetEnvironmentVariable("LLM_LIMITS_BACKEND");
            if (!string.IsNullOrEmpty(envB)) {
                string b = envB.Trim().ToLowerInvariant();
                if (b == "auto" || b == "windows" || b == "wsl") {
                    return new BackendResolutionResult { Backend = b, Source = "env LLM_LIMITS_BACKEND" };
                }
            }

            if (config != null && !string.IsNullOrEmpty(config.Backend)) {
                string b = config.Backend.Trim().ToLowerInvariant();
                if (b == "auto" || b == "windows" || b == "wsl") {
                    return new BackendResolutionResult { Backend = b, Source = "overlay.config.json" };
                }
            }

            return new BackendResolutionResult { Backend = "auto", Source = "default" };
        }

        public static PythonCandidate TestPythonCandidate(string cmd, string argsPrefix) {
            try {
                var psi = new ProcessStartInfo {
                    FileName = cmd,
                    Arguments = (string.IsNullOrEmpty(argsPrefix) ? "" : argsPrefix + " ") + "-c \"import sys;print(sys.executable)\"",
                    UseShellExecute = false,
                    CreateNoWindow = true,
                    RedirectStandardOutput = true,
                    RedirectStandardError = true
                };

                using (var proc = Process.Start(psi)) {
                    string outText = proc.StandardOutput.ReadToEnd().Trim();
                    if (proc.WaitForExit(3000) && proc.ExitCode == 0 && File.Exists(outText)) {
                        return new PythonCandidate {
                            Command = cmd,
                            PrefixArgs = argsPrefix,
                            Executable = outText
                        };
                    } else if (!proc.HasExited) {
                        try { proc.Kill(); } catch { }
                    }
                }
            } catch { }
            return null;
        }

        public static PythonCandidate ResolveNativePython(Config config) {
            lock (_syncLock) {
                if (_cachedNativePython != null) {
                    return _cachedNativePython;
                }

                // 0. Config pythonPath
                if (config != null && !string.IsNullOrEmpty(config.PythonPath)) {
                    var t = TestPythonCandidate(config.PythonPath.Trim(), null);
                    if (t != null) return _cachedNativePython = t;
                }

                // 1. LLM_LIMITS_PYTHON env
                string envPy = Environment.GetEnvironmentVariable("LLM_LIMITS_PYTHON");
                if (!string.IsNullOrEmpty(envPy)) {
                    var t = TestPythonCandidate(envPy.Trim(), null);
                    if (t != null) return _cachedNativePython = t;
                }

                // 2. py -3
                var tPy = TestPythonCandidate("py", "-3");
                if (tPy != null) return _cachedNativePython = tPy;

                // 3. python.exe
                var tPyExe = TestPythonCandidate("python.exe", null);
                if (tPyExe != null) return _cachedNativePython = tPyExe;

                // 4. python3.exe
                var tPy3Exe = TestPythonCandidate("python3.exe", null);
                if (tPy3Exe != null) return _cachedNativePython = tPy3Exe;

                // 5. Look in %LOCALAPPDATA%\Programs\Python, %LOCALAPPDATA%\Python, C:\Python3*
                var candidates = new List<string>();
                string localAppData = Environment.GetFolderPath(Environment.SpecialFolder.LocalApplicationData);
                if (!string.IsNullOrEmpty(localAppData)) {
                    string progPy = Path.Combine(localAppData, @"Programs\Python");
                    if (Directory.Exists(progPy)) {
                        try {
                            candidates.AddRange(Directory.GetFiles(progPy, "python.exe", SearchOption.AllDirectories));
                        } catch { }
                    }
                    string rootPy = Path.Combine(localAppData, "Python");
                    if (Directory.Exists(rootPy)) {
                        try {
                            candidates.AddRange(Directory.GetFiles(rootPy, "python.exe", SearchOption.AllDirectories));
                        } catch { }
                    }
                }

                try {
                    string[] cDirs = Directory.GetDirectories(@"C:\", "Python3*");
                    foreach (string cd in cDirs) {
                        string exe = Path.Combine(cd, "python.exe");
                        if (File.Exists(exe)) candidates.Add(exe);
                    }
                } catch { }

                foreach (string cand in candidates) {
                    if (File.Exists(cand)) {
                        var t = TestPythonCandidate(cand, null);
                        if (t != null) return _cachedNativePython = t;
                    }
                }

                return null;
            }
        }

        public static string ResolveWslDistro(Config config) {
            lock (_syncLock) {
                if (!string.IsNullOrEmpty(_cachedWslDistro)) {
                    return _cachedWslDistro;
                }

                string envWsl = Environment.GetEnvironmentVariable("LLM_LIMITS_WSL_DISTRO");
                if (!string.IsNullOrEmpty(envWsl)) {
                    return _cachedWslDistro = envWsl.Trim();
                }

                string envAgy = Environment.GetEnvironmentVariable("AGY_WSL_DISTRO");
                if (!string.IsNullOrEmpty(envAgy)) {
                    return _cachedWslDistro = envAgy.Trim();
                }

                if (config != null && !string.IsNullOrEmpty(config.WslDistro)) {
                    return _cachedWslDistro = config.WslDistro.Trim();
                }

                try {
                    var psi = new ProcessStartInfo {
                        FileName = "wsl.exe",
                        Arguments = "-l -q",
                        UseShellExecute = false,
                        CreateNoWindow = true,
                        RedirectStandardOutput = true,
                        RedirectStandardError = true
                    };

                    using (var proc = Process.Start(psi)) {
                        string rawList = proc.StandardOutput.ReadToEnd();
                        proc.WaitForExit(5000);
                        if (proc.ExitCode == 0 && !string.IsNullOrEmpty(rawList)) {
                            // wsl.exe outputs UTF-16 with null bytes often
                            string cleaned = rawList.Replace("\0", "");
                            string[] lines = cleaned.Split(new char[] { '\r', '\n' }, StringSplitOptions.RemoveEmptyEntries);
                            var distros = new List<string>();
                            foreach (string line in lines) {
                                string d = line.Trim();
                                if (!string.IsNullOrEmpty(d) && !d.StartsWith("docker-desktop", StringComparison.OrdinalIgnoreCase)) {
                                    distros.Add(d);
                                }
                            }

                            foreach (string distro in distros) {
                                try {
                                    var testPsi = new ProcessStartInfo {
                                        FileName = "wsl.exe",
                                        Arguments = "-d " + distro + " which python3",
                                        UseShellExecute = false,
                                        CreateNoWindow = true,
                                        RedirectStandardOutput = true,
                                        RedirectStandardError = true
                                    };

                                    using (var testProc = Process.Start(testPsi)) {
                                        string outText = testProc.StandardOutput.ReadToEnd().Trim();
                                        if (testProc.WaitForExit(25000) && testProc.ExitCode == 0) {
                                            if (!string.IsNullOrEmpty(outText) && !outText.Contains("no python3")) {
                                                Logger.Log("Auto-detected WSL distro with python3: " + distro + " (" + outText + ")");
                                                return _cachedWslDistro = distro;
                                            }
                                        } else if (!testProc.HasExited) {
                                            try { testProc.Kill(); } catch { }
                                        }
                                    }
                                } catch { }
                            }
                        }
                    }
                } catch (Exception ex) {
                    Logger.Log("Error querying wsl.exe distros: " + ex.Message);
                }

                return null;
            }
        }

        public static string ResolveWslFetchScript(string distro, string appDirectory) {
            string envWslScript = Environment.GetEnvironmentVariable("LLM_LIMITS_WSL_SCRIPT");
            if (!string.IsNullOrEmpty(envWslScript)) return envWslScript;

            string envAgyScript = Environment.GetEnvironmentVariable("AGY_WSL_SCRIPT");
            if (!string.IsNullOrEmpty(envAgyScript)) return envAgyScript;

            if (string.IsNullOrEmpty(distro)) return "fetch_quota.py";

            string localPy = Path.Combine(appDirectory, "fetch_quota.py");
            if (File.Exists(localPy)) {
                try {
                    string p = localPy.Replace('\\', '/');
                    var psi = new ProcessStartInfo {
                        FileName = "wsl.exe",
                        Arguments = "-d " + distro + " wslpath -u \"" + p + "\"",
                        UseShellExecute = false,
                        CreateNoWindow = true,
                        RedirectStandardOutput = true,
                        RedirectStandardError = true
                    };
                    using (var proc = Process.Start(psi)) {
                        string wslPath = proc.StandardOutput.ReadToEnd().Trim();
                        if (proc.WaitForExit(3000) && proc.ExitCode == 0 && !string.IsNullOrEmpty(wslPath)) {
                            return wslPath;
                        }
                    }
                } catch { }
            }

            try {
                var psi = new ProcessStartInfo {
                    FileName = "wsl.exe",
                    Arguments = "-d " + distro + " -e bash -lc \"echo $HOME\"",
                    UseShellExecute = false,
                    CreateNoWindow = true,
                    RedirectStandardOutput = true,
                    RedirectStandardError = true
                };
                using (var proc = Process.Start(psi)) {
                    string wslHome = proc.StandardOutput.ReadToEnd().Trim();
                    if (proc.WaitForExit(5000) && proc.ExitCode == 0 && !string.IsNullOrEmpty(wslHome)) {
                        string[] candidates = new string[] {
                            wslHome + "/llm-limits-overlay/fetch_quota.py",
                            wslHome + "/agy-limits-overlay/fetch_quota.py"
                        };
                        foreach (string c in candidates) {
                            var chkPsi = new ProcessStartInfo {
                                FileName = "wsl.exe",
                                Arguments = "-d " + distro + " test -f \"" + c + "\"",
                                UseShellExecute = false,
                                CreateNoWindow = true
                            };
                            using (var chkProc = Process.Start(chkPsi)) {
                                if (chkProc.WaitForExit(3000) && chkProc.ExitCode == 0) {
                                    return c;
                                }
                            }
                        }
                        return wslHome + "/llm-limits-overlay/fetch_quota.py";
                    }
                }
            } catch { }

            return "fetch_quota.py";
        }
    }
}
