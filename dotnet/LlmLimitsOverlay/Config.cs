using System;
using System.Collections.Generic;
using System.IO;
using System.Text;

namespace LlmLimitsOverlay {
    public class Config {
        private static readonly object _syncLock = new object();
        private static string _configFilePath;

        public string Backend { get; set; }
        public string PythonPath { get; set; }
        public string WslDistro { get; set; }
        public double TimeoutSeconds { get; set; }
        public bool TestHooks { get; set; }
        public string ClickThroughHotkey { get; set; }
        public bool ClickThrough { get; set; }

        public Config() {
            Backend = "windows";
            PythonPath = "";
            WslDistro = "";
            TimeoutSeconds = 0;
            TestHooks = false;
            ClickThroughHotkey = "Ctrl+Alt+L";
            ClickThrough = true;
        }

        public static void Initialize(string appDirectory) {
            _configFilePath = Path.Combine(appDirectory, "overlay.config.json");
        }

        public static Config Load() {
            var cfg = new Config();
            if (string.IsNullOrEmpty(_configFilePath) || !File.Exists(_configFilePath)) {
                return cfg;
            }

            try {
                string raw;
                lock (_syncLock) {
                    raw = File.ReadAllText(_configFilePath, Encoding.UTF8);
                }

                var dict = SimpleJson.Deserialize(raw) as Dictionary<string, object>;
                if (dict != null) {
                    cfg.Backend = SimpleJson.GetString(dict, "backend", "windows").ToLowerInvariant();
                    cfg.PythonPath = SimpleJson.GetString(dict, "pythonPath", "");
                    cfg.WslDistro = SimpleJson.GetString(dict, "wslDistro", "");
                    cfg.TimeoutSeconds = SimpleJson.GetDouble(dict, "timeoutSeconds", 0);
                    cfg.TestHooks = SimpleJson.GetBool(dict, "testHooks", false);
                    cfg.ClickThroughHotkey = SimpleJson.GetString(dict, "clickThroughHotkey", "Ctrl+Alt+L");
                    cfg.ClickThrough = SimpleJson.GetBool(dict, "clickThrough", true);
                }
            } catch (Exception ex) {
                Logger.Log("Error reading " + _configFilePath + ": " + ex.Message);
            }

            return cfg;
        }

        public static void SaveField(string field, object value) {
            if (string.IsNullOrEmpty(_configFilePath)) return;

            try {
                lock (_syncLock) {
                    Dictionary<string, object> dict = null;
                    if (File.Exists(_configFilePath)) {
                        try {
                            string raw = File.ReadAllText(_configFilePath, Encoding.UTF8);
                            dict = SimpleJson.Deserialize(raw) as Dictionary<string, object>;
                        } catch { }
                    }

                    if (dict == null) {
                        dict = new Dictionary<string, object>(StringComparer.OrdinalIgnoreCase);
                        dict["backend"] = "windows";
                        dict["pythonPath"] = "";
                        dict["wslDistro"] = "";
                        dict["timeoutSeconds"] = 0;
                        dict["testHooks"] = false;
                        dict["clickThroughHotkey"] = "Ctrl+Alt+L";
                        dict["clickThrough"] = true;
                    }

                    dict[field] = value;
                    string json = SimpleJson.Serialize(dict, true);
                    File.WriteAllText(_configFilePath, json, Encoding.UTF8);
                }
            } catch (Exception ex) {
                Logger.Log("Error saving config (" + field + "=" + value + "): " + ex.Message);
            }
        }
    }
}
