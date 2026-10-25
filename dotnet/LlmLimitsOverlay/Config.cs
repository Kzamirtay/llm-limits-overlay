using System;
using System.Collections.Generic;
using System.Globalization;
using System.IO;
using System.Text;

namespace LlmLimitsOverlay {
    public class Config {
        private static readonly object _syncLock = new object();
        private static string _configFilePath;

        private static readonly string[] CanonicalKeyOrder = new string[] {
            "backend",
            "pythonPath",
            "wslDistro",
            "timeoutSeconds",
            "testHooks",
            "clickThroughHotkey",
            "clickThrough"
        };

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

        public static string SerializePsCompatible(Dictionary<string, object> dict) {
            StringBuilder sb = new StringBuilder();
            sb.Append("{\r\n");

            List<string> keys = new List<string>();
            foreach (string k in CanonicalKeyOrder) {
                foreach (string existingKey in dict.Keys) {
                    if (string.Equals(existingKey, k, StringComparison.OrdinalIgnoreCase)) {
                        keys.Add(existingKey);
                        break;
                    }
                }
            }
            foreach (string existingKey in dict.Keys) {
                bool found = false;
                foreach (string k in CanonicalKeyOrder) {
                    if (string.Equals(existingKey, k, StringComparison.OrdinalIgnoreCase)) {
                        found = true;
                        break;
                    }
                }
                if (!found) {
                    keys.Add(existingKey);
                }
            }

            for (int i = 0; i < keys.Count; i++) {
                string key = keys[i];
                object val = dict[key];

                sb.Append("    \"");
                sb.Append(key);
                sb.Append("\":  ");

                if (val == null) {
                    sb.Append("null");
                } else if (val is bool) {
                    sb.Append((bool)val ? "true" : "false");
                } else if (val is int || val is long || val is short || val is byte) {
                    sb.Append(val.ToString());
                } else if (val is double || val is float || val is decimal) {
                    double d = Convert.ToDouble(val);
                    if (Math.Floor(d) == d) {
                        sb.Append(((long)d).ToString());
                    } else {
                        sb.Append(d.ToString(CultureInfo.InvariantCulture));
                    }
                } else {
                    sb.Append("\"");
                    string s = val.ToString();
                    for (int j = 0; j < s.Length; j++) {
                        char c = s[j];
                        if (c == '"') sb.Append("\\\"");
                        else if (c == '\\') sb.Append("\\\\");
                        else if (c == '\r') sb.Append("\\r");
                        else if (c == '\n') sb.Append("\\n");
                        else if (c == '\t') sb.Append("\\t");
                        else sb.Append(c);
                    }
                    sb.Append("\"");
                }

                if (i < keys.Count - 1) {
                    sb.Append(",");
                }
                sb.Append("\r\n");
            }

            sb.Append("}");
            return sb.ToString();
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

                    string matchedKey = field;
                    foreach (string k in CanonicalKeyOrder) {
                        if (string.Equals(k, field, StringComparison.OrdinalIgnoreCase)) {
                            matchedKey = k;
                            break;
                        }
                    }
                    dict[matchedKey] = value;

                    string json = SerializePsCompatible(dict);
                    File.WriteAllText(_configFilePath, json, new UTF8Encoding(true));
                }
            } catch (Exception ex) {
                Logger.Log("Error saving config (" + field + "=" + value + "): " + ex.Message);
            }
        }
    }
}
