using System;
using System.IO;

namespace LlmLimitsOverlay {
    public static class Logger {
        private static readonly object _syncLock = new object();
        private static string _logFilePath;

        public static void Initialize(string appDirectory) {
            _logFilePath = Path.Combine(appDirectory, "overlay.log");
        }

        public static void Log(string message) {
            try {
                string ts = DateTime.Now.ToString("yyyy-MM-dd HH:mm:ss");
                string line = "[" + ts + "] " + message;

                if (!string.IsNullOrEmpty(_logFilePath)) {
                    lock (_syncLock) {
                        File.AppendAllText(_logFilePath, line + Environment.NewLine);
                    }
                }
            } catch {
                // Logging failures must not crash the application
            }
        }
    }
}
