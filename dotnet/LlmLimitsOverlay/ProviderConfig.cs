using System;
using System.Collections.Generic;

namespace LlmLimitsOverlay {
    // Описание провайдера из overlay.config.json ("providers": [...]).
    // Поля: id, type (kimi|openrouter|agy), title, apiKeyEnv|apiKey, enabled,
    // опционально baseUrl как override для прокси/зеркала (обычно не нужен).
    // НЕИЗВЕСТНЫЙ type — запись пропускается с предупреждением в лог (новый
    // провайдер = новый тип в коде).
    public class ProviderConfig {
        public string Id = "";
        public string Type = "kimi";     // kimi | openrouter | agy
        public string Title = "";
        public string BaseUrl = "";      // опционально; дефолт — официальный эндпоинт типа
        public string ApiKeyEnv = "";
        public string ApiKey = "";
        public bool Enabled = true;

        public static List<ProviderConfig> GetDefaults() {
            return new List<ProviderConfig> {
                new ProviderConfig { Id = "kimi", Type = "kimi", Title = "KIMI",
                    BaseUrl = "https://api.kimi.com/coding/v1", ApiKeyEnv = "KIMI_API_KEY" },
                new ProviderConfig { Id = "openrouter", Type = "openrouter", Title = "OPENROUTER",
                    BaseUrl = "https://openrouter.ai/api/v1", ApiKeyEnv = "OPENROUTER_API_KEY" },
                new ProviderConfig { Id = "agy", Type = "agy", Title = "GEMINI + CLAUDE/GPT" }
            };
        }

        public static List<ProviderConfig> ParseList(object raw) {
            var result = new List<ProviderConfig>();
            var list = raw as List<object>;
            if (list == null) return result;

            int autoIdx = 0;
            foreach (object item in list) {
                var d = item as Dictionary<string, object>;
                if (d == null) continue;

                var p = new ProviderConfig();
                p.Id = SimpleJson.GetString(d, "id", "").Trim().ToLowerInvariant();
                if (p.Id.Length == 0) {
                    autoIdx++;
                    p.Id = "provider" + autoIdx;
                }
                p.Type = SimpleJson.GetString(d, "type", "kimi").Trim().ToLowerInvariant();
                if (p.Type != "kimi" && p.Type != "openrouter" && p.Type != "agy") {
                    Logger.Log(string.Format("[config] providers: неизвестный type '{0}' у записи '{1}' — запись пропущена (новый провайдер = новый тип в коде)",
                        p.Type, p.Id));
                    continue;
                }
                p.Title = SimpleJson.GetString(d, "title", p.Id.ToUpperInvariant());
                p.BaseUrl = SimpleJson.GetString(d, "baseUrl", "").Trim().TrimEnd('/');
                p.ApiKeyEnv = SimpleJson.GetString(d, "apiKeyEnv", "").Trim();
                p.ApiKey = SimpleJson.GetString(d, "apiKey", "").Trim();
                p.Enabled = !d.ContainsKey("enabled") || SimpleJson.GetBool(d, "enabled", true);
                result.Add(p);
            }
            return result;
        }

        public static object SerializeList(List<ProviderConfig> providers) {
            var list = new List<object>();
            foreach (var p in providers) {
                var d = new Dictionary<string, object>(StringComparer.OrdinalIgnoreCase);
                d["id"] = p.Id;
                d["type"] = p.Type;
                d["title"] = p.Title;
                if (!string.IsNullOrEmpty(p.BaseUrl)) d["baseUrl"] = p.BaseUrl;
                if (!string.IsNullOrEmpty(p.ApiKeyEnv)) d["apiKeyEnv"] = p.ApiKeyEnv;
                if (!string.IsNullOrEmpty(p.ApiKey)) d["apiKey"] = p.ApiKey;
                if (!p.Enabled) d["enabled"] = false;
                list.Add(d);
            }
            return list;
        }

        // Разрешение ключа: apiKey из конфига (если задан), иначе переменная apiKeyEnv из .env/окружения.
        public string ResolveApiKey(EnvParser env) {
            if (!string.IsNullOrEmpty(ApiKey)) return ApiKey;
            if (!string.IsNullOrEmpty(ApiKeyEnv) && env != null) return env.Get(ApiKeyEnv);
            return null;
        }
    }
}
