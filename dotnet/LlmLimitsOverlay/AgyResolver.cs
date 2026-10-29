using System;
using System.Diagnostics;
using System.IO;
using System.Runtime.InteropServices;

namespace LlmLimitsOverlay {
    public static class AgyResolver {
        private static string _cachedAgyBin = null;
        private static readonly object _syncLock = new object();

        private static bool IsWindows() {
            return RuntimeInformation.IsOSPlatform(OSPlatform.Windows);
        }

        public static void ClearCache() {
            lock (_syncLock) {
                _cachedAgyBin = null;
            }
        }

        public static bool CheckAgyAuth() {
            if (!IsWindows()) {
                // Linux: токен только в домашней директории
                string home = Environment.GetFolderPath(Environment.SpecialFolder.UserProfile);
                if (!string.IsNullOrEmpty(home)) {
                    string tokPath = Path.Combine(home, @".gemini/antigravity-cli/antigravity-oauth-token");
                    if (File.Exists(tokPath)) {
                        try {
                            var fi = new FileInfo(tokPath);
                            if (fi.Length > 0) return true;
                        } catch { }
                    }
                }
                return false;
            }

            string userProfile = Environment.GetFolderPath(Environment.SpecialFolder.UserProfile);
            if (!string.IsNullOrEmpty(userProfile)) {
                string tokPath = Path.Combine(userProfile, @".gemini\antigravity-cli\antigravity-oauth-token");
                if (File.Exists(tokPath)) {
                    try {
                        var fi = new FileInfo(tokPath);
                        if (fi.Length > 0) return true;
                    } catch { }
                }
            }

            string localAppData = Environment.GetFolderPath(Environment.SpecialFolder.LocalApplicationData);
            if (!string.IsNullOrEmpty(localAppData)) {
                string tokPath2 = Path.Combine(localAppData, @".gemini\antigravity-cli\antigravity-oauth-token");
                if (File.Exists(tokPath2)) {
                    try {
                        var fi = new FileInfo(tokPath2);
                        if (fi.Length > 0) return true;
                    } catch { }
                }
            }

            return false;
        }

        public static string FindAgyBinary() {
            lock (_syncLock) {
                if (_cachedAgyBin != null && File.Exists(_cachedAgyBin)) {
                    return _cachedAgyBin;
                }

                // 1. env LLM_LIMITS_AGY_BIN
                string envBin = Environment.GetEnvironmentVariable("LLM_LIMITS_AGY_BIN");
                if (!string.IsNullOrEmpty(envBin) && File.Exists(envBin)) {
                    return _cachedAgyBin = envBin;
                }

                // 2. PATH resolution
                string pathEnv = Environment.GetEnvironmentVariable("PATH");
                if (!string.IsNullOrEmpty(pathEnv)) {
                    char sep = IsWindows() ? ';' : ':';
                    string[] parts = pathEnv.Split(sep);
                    foreach (string rawDir in parts) {
                        string dir = rawDir.Trim();
                        if (string.IsNullOrEmpty(dir)) continue;

                        try {
                            if (IsWindows()) {
                                string cand1 = Path.Combine(dir, "agy.exe");
                                if (File.Exists(cand1)) return _cachedAgyBin = cand1;
                            }
                            string cand2 = Path.Combine(dir, "agy");
                            if (File.Exists(cand2)) return _cachedAgyBin = cand2;
                        } catch { }
                    }
                }

                if (!IsWindows()) {
                    // 3. Linux: well-known install locations
                    string home = Environment.GetFolderPath(Environment.SpecialFolder.UserProfile);
                    if (!string.IsNullOrEmpty(home)) {
                        string cand = Path.Combine(home, @".local/bin/agy");
                        if (File.Exists(cand)) return _cachedAgyBin = cand;
                    }
                    if (File.Exists(@"/usr/local/bin/agy")) return _cachedAgyBin = @"/usr/local/bin/agy";
                    return null;
                }

                // 3. Known LocalAppData path: %LOCALAPPDATA%\agy\bin\agy.exe
                string localAppData = Environment.GetFolderPath(Environment.SpecialFolder.LocalApplicationData);
                if (!string.IsNullOrEmpty(localAppData)) {
                    string cand = Path.Combine(localAppData, @"agy\bin\agy.exe");
                    if (File.Exists(cand)) return _cachedAgyBin = cand;
                }

                // 4. Known UserProfile path: %USERPROFILE%\AppData\Local\agy\bin\agy.exe
                string userProfile = Environment.GetFolderPath(Environment.SpecialFolder.UserProfile);
                if (!string.IsNullOrEmpty(userProfile)) {
                    string cand = Path.Combine(userProfile, @"AppData\Local\agy\bin\agy.exe");
                    if (File.Exists(cand)) return _cachedAgyBin = cand;
                }

                return null;
            }
        }
    }
}
