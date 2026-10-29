using System;
using System.Collections.Generic;
using System.IO;
using System.Text;

namespace LlmLimitsOverlay {
    public class EnvParser {
        private readonly Dictionary<string, string> _entries;

        public EnvParser(string envPath) {
            _entries = ParseFile(envPath);
        }

        public string Get(string key) {
            if (string.IsNullOrEmpty(key)) return null;

            string val;
            if (_entries != null && _entries.TryGetValue(key, out val)) {
                if (!string.IsNullOrEmpty(val)) {
                    return val;
                }
            }

            string envVal = Environment.GetEnvironmentVariable(key);
            if (!string.IsNullOrEmpty(envVal)) {
                return envVal.Trim();
            }

            return null;
        }

        // Возвращает true, если значение ключа — пустое, плейсхолдер из .env.example,
        // или явно не настоящий ключ (содержит ..., here, xxxx и т.п.).
        public static bool IsPlaceholderKey(string value) {
            if (string.IsNullOrEmpty(value)) return true;

            string v = value.Trim().Trim('"', '\'').Trim();
            if (v.Length == 0) return true;

            string lower = v.ToLowerInvariant();

            // Признаки плейсхолдера: многоточие, слово here/xxxx, известные образцы
            if (lower.Contains("...") || lower.Contains("here") || lower.Contains("xxxx")) return true;

            // Образцы из .env.example (в любом регистре)
            if (lower == "sk-or-v1-your-openrouter-key-here") return true;
            if (lower == "sk-your-kimi-key-here") return true;
            if (lower.StartsWith("sk-or-v1-") && lower.EndsWith("-here")) return true;

            return false;
        }

        public static string ParseEnvValue(string rawVal) {
            if (rawVal == null) return "";
            rawVal = rawVal.Trim();
            if (rawVal.Length == 0) return "";

            char quoteChar = rawVal[0];
            if (quoteChar == '"' || quoteChar == '\'') {
                StringBuilder sb = new StringBuilder();
                int i = 1;
                int n = rawVal.Length;
                bool closed = false;

                while (i < n) {
                    char ch = rawVal[i];
                    if (ch == '\\' && quoteChar == '"' && i + 1 < n) {
                        char nextCh = rawVal[i + 1];
                        if (nextCh == '"' || nextCh == '\\' || nextCh == '$' || nextCh == '`') {
                            sb.Append(nextCh);
                            i += 2;
                            continue;
                        } else if (nextCh == 'n') {
                            sb.Append('\n');
                            i += 2;
                            continue;
                        } else if (nextCh == 'r') {
                            sb.Append('\r');
                            i += 2;
                            continue;
                        } else if (nextCh == 't') {
                            sb.Append('\t');
                            i += 2;
                            continue;
                        }
                    }

                    if (ch == quoteChar) {
                        closed = true;
                        break;
                    }
                    sb.Append(ch);
                    i++;
                }

                if (closed) {
                    return sb.ToString().Trim();
                } else {
                    return rawVal.Substring(1).Trim();
                }
            } else {
                int commentIdx = rawVal.IndexOf('#');
                if (commentIdx != -1) {
                    rawVal = rawVal.Substring(0, commentIdx);
                }
                return rawVal.Trim();
            }
        }

        public static Dictionary<string, string> ParseFile(string envPath) {
            var result = new Dictionary<string, string>(StringComparer.OrdinalIgnoreCase);
            if (string.IsNullOrEmpty(envPath) || !File.Exists(envPath)) {
                return result;
            }

            try {
                using (var stream = new FileStream(envPath, FileMode.Open, FileAccess.Read, FileShare.ReadWrite))
                using (var reader = new StreamReader(stream, Encoding.UTF8)) {
                    string line;
                    while ((line = reader.ReadLine()) != null) {
                        line = line.Trim();
                        if (line.Length == 0 || line.StartsWith("#")) {
                            continue;
                        }

                        if (line.StartsWith("export", StringComparison.OrdinalIgnoreCase)) {
                            string rem = line.Substring(6);
                            if (rem.Length > 0 && char.IsWhiteSpace(rem[0])) {
                                line = rem.TrimStart();
                            }
                        }

                        int eqIdx = line.IndexOf('=');
                        if (eqIdx == -1) {
                            continue;
                        }

                        string key = line.Substring(0, eqIdx).Trim();
                        string rawVal = line.Substring(eqIdx + 1).Trim();

                        if (key.Length == 0) {
                            continue;
                        }

                        string val = ParseEnvValue(rawVal);
                        result[key] = val;
                    }
                }
            } catch (Exception ex) {
                Logger.Log("Error reading .env: " + ex.Message);
            }

            return result;
        }
    }
}
