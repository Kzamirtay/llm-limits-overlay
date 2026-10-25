using System;
using System.Collections;
using System.Collections.Generic;
using System.Globalization;
using System.Text;

namespace LlmLimitsOverlay {
    public class SimpleJson {
        private readonly string _src;
        private int _idx;

        public static object Deserialize(string json) {
            if (string.IsNullOrEmpty(json)) return null;
            return new SimpleJson(json).ParseValue();
        }

        private SimpleJson(string src) {
            _src = src;
            _idx = 0;
        }

        private void SkipWhitespace() {
            while (_idx < _src.Length && char.IsWhiteSpace(_src[_idx])) {
                _idx++;
            }
        }

        private char Peek() {
            SkipWhitespace();
            return _idx < _src.Length ? _src[_idx] : '\0';
        }

        private char Next() {
            SkipWhitespace();
            return _idx < _src.Length ? _src[_idx++] : '\0';
        }

        private object ParseValue() {
            char c = Peek();
            if (c == '{') return ParseObject();
            if (c == '[') return ParseArray();
            if (c == '"') return ParseString();
            if (c == 't' || c == 'f') return ParseBool();
            if (c == 'n') return ParseNull();
            if (c == '-' || char.IsDigit(c)) return ParseNumber();
            return null;
        }

        private Dictionary<string, object> ParseObject() {
            var obj = new Dictionary<string, object>(StringComparer.OrdinalIgnoreCase);
            Next(); // consume '{'
            while (true) {
                char c = Peek();
                if (c == '}' || c == '\0') {
                    if (c == '}') Next();
                    break;
                }
                if (c == ',') {
                    Next();
                    continue;
                }
                string key = ParseString();
                SkipWhitespace();
                if (Peek() == ':') Next();
                object val = ParseValue();
                obj[key] = val;
            }
            return obj;
        }

        private List<object> ParseArray() {
            var list = new List<object>();
            Next(); // consume '['
            while (true) {
                char c = Peek();
                if (c == ']' || c == '\0') {
                    if (c == ']') Next();
                    break;
                }
                if (c == ',') {
                    Next();
                    continue;
                }
                list.Add(ParseValue());
            }
            return list;
        }

        private string ParseString() {
            Next(); // consume '"'
            var sb = new StringBuilder();
            while (_idx < _src.Length) {
                char c = _src[_idx++];
                if (c == '"') break;
                if (c == '\\' && _idx < _src.Length) {
                    char esc = _src[_idx++];
                    switch (esc) {
                        case '"': sb.Append('"'); break;
                        case '\\': sb.Append('\\'); break;
                        case '/': sb.Append('/'); break;
                        case 'b': sb.Append('\b'); break;
                        case 'f': sb.Append('\f'); break;
                        case 'n': sb.Append('\n'); break;
                        case 'r': sb.Append('\r'); break;
                        case 't': sb.Append('\t'); break;
                        case 'u':
                            if (_idx + 4 <= _src.Length) {
                                string hex = _src.Substring(_idx, 4);
                                _idx += 4;
                                int code;
                                if (int.TryParse(hex, NumberStyles.HexNumber, CultureInfo.InvariantCulture, out code)) {
                                    sb.Append((char)code);
                                }
                            }
                            break;
                        default: sb.Append(esc); break;
                    }
                } else {
                    sb.Append(c);
                }
            }
            return sb.ToString();
        }

        private bool ParseBool() {
            if (_src.Substring(_idx).StartsWith("true", StringComparison.OrdinalIgnoreCase)) {
                _idx += 4;
                return true;
            }
            if (_src.Substring(_idx).StartsWith("false", StringComparison.OrdinalIgnoreCase)) {
                _idx += 5;
                return false;
            }
            return false;
        }

        private object ParseNull() {
            if (_src.Substring(_idx).StartsWith("null", StringComparison.OrdinalIgnoreCase)) {
                _idx += 4;
            }
            return null;
        }

        private object ParseNumber() {
            int start = _idx;
            if (_idx < _src.Length && _src[_idx] == '-') _idx++;
            while (_idx < _src.Length && (char.IsDigit(_src[_idx]) || _src[_idx] == '.' || _src[_idx] == 'e' || _src[_idx] == 'E' || _src[_idx] == '+' || _src[_idx] == '-')) {
                _idx++;
            }
            string numStr = _src.Substring(start, _idx - start);
            double d;
            if (double.TryParse(numStr, NumberStyles.Float, CultureInfo.InvariantCulture, out d)) {
                return d;
            }
            return 0.0;
        }

        public static string Serialize(object obj, bool pretty) {
            var sb = new StringBuilder();
            SerializeValue(obj, sb, pretty ? 0 : -1);
            return sb.ToString();
        }

        private static void SerializeValue(object obj, StringBuilder sb, int indent) {
            if (obj == null) {
                sb.Append("null");
                return;
            }

            if (obj is string) {
                sb.Append('"');
                string s = (string)obj;
                for (int i = 0; i < s.Length; i++) {
                    char c = s[i];
                    switch (c) {
                        case '"': sb.Append("\\\""); break;
                        case '\\': sb.Append("\\\\"); break;
                        case '\b': sb.Append("\\b"); break;
                        case '\f': sb.Append("\\f"); break;
                        case '\n': sb.Append("\\n"); break;
                        case '\r': sb.Append("\\r"); break;
                        case '\t': sb.Append("\\t"); break;
                        default:
                            if (c < 32) {
                                sb.AppendFormat(CultureInfo.InvariantCulture, "\\u{0:x4}", (int)c);
                            } else {
                                sb.Append(c);
                            }
                            break;
                    }
                }
                sb.Append('"');
                return;
            }

            if (obj is bool) {
                sb.Append((bool)obj ? "true" : "false");
                return;
            }

            if (obj is double || obj is float || obj is decimal) {
                sb.Append(Convert.ToString(obj, CultureInfo.InvariantCulture));
                return;
            }

            if (obj is int || obj is long || obj is short || obj is byte || obj is uint || obj is ulong) {
                sb.Append(Convert.ToString(obj, CultureInfo.InvariantCulture));
                return;
            }

            if (obj is IDictionary) {
                IDictionary dict = (IDictionary)obj;
                sb.Append('{');
                bool first = true;
                int nextIndent = indent >= 0 ? indent + 4 : -1;

                foreach (DictionaryEntry kv in dict) {
                    if (!first) sb.Append(',');
                    first = false;
                    if (indent >= 0) {
                        sb.AppendLine();
                        sb.Append(new string(' ', nextIndent));
                    }
                    SerializeValue(kv.Key != null ? kv.Key.ToString() : "", sb, nextIndent);
                    sb.Append(indent >= 0 ? ": " : ":");
                    SerializeValue(kv.Value, sb, nextIndent);
                }

                if (!first && indent >= 0) {
                    sb.AppendLine();
                    sb.Append(new string(' ', indent));
                }
                sb.Append('}');
                return;
            }

            if (obj is IEnumerable) {
                IEnumerable list = (IEnumerable)obj;
                sb.Append('[');
                bool first = true;
                int nextIndent = indent >= 0 ? indent + 4 : -1;

                foreach (object item in list) {
                    if (!first) sb.Append(',');
                    first = false;
                    if (indent >= 0) {
                        sb.AppendLine();
                        sb.Append(new string(' ', nextIndent));
                    }
                    SerializeValue(item, sb, nextIndent);
                }

                if (!first && indent >= 0) {
                    sb.AppendLine();
                    sb.Append(new string(' ', indent));
                }
                sb.Append(']');
                return;
            }

            sb.Append('"');
            sb.Append(obj.ToString());
            sb.Append('"');
        }

        // Helper getters
        public static string GetString(Dictionary<string, object> dict, string key, string defVal) {
            if (dict == null || !dict.ContainsKey(key) || dict[key] == null) return defVal;
            return dict[key].ToString();
        }

        public static double GetDouble(Dictionary<string, object> dict, string key, double defVal) {
            if (dict == null || !dict.ContainsKey(key) || dict[key] == null) return defVal;
            object val = dict[key];
            if (val is double) return (double)val;
            double d;
            if (double.TryParse(val.ToString(), NumberStyles.Float, CultureInfo.InvariantCulture, out d)) {
                return d;
            }
            return defVal;
        }

        public static int GetInt(Dictionary<string, object> dict, string key, int defVal) {
            return (int)Math.Round(GetDouble(dict, key, defVal));
        }

        public static bool GetBool(Dictionary<string, object> dict, string key, bool defVal) {
            if (dict == null || !dict.ContainsKey(key) || dict[key] == null) return defVal;
            object val = dict[key];
            if (val is bool) return (bool)val;
            string s = val.ToString().Trim().ToLowerInvariant();
            if (s == "true" || s == "1") return true;
            if (s == "false" || s == "0") return false;
            return defVal;
        }

        public static Dictionary<string, object> GetObject(Dictionary<string, object> dict, string key) {
            if (dict == null || !dict.ContainsKey(key) || dict[key] == null) return null;
            return dict[key] as Dictionary<string, object>;
        }

        public static List<object> GetList(Dictionary<string, object> dict, string key) {
            if (dict == null || !dict.ContainsKey(key) || dict[key] == null) return null;
            return dict[key] as List<object>;
        }
    }
}
