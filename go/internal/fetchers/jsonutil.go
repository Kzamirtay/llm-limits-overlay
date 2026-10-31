package fetchers

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

func marshalPretty(v interface{}) ([]byte, error) {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return nil, err
	}
	return b, nil
}

// getStr/getFloat/getInt/getMap/getList — безопасные getter'ы для map[string]interface{}.
func getStr(d map[string]interface{}, key string) string {
	if v, ok := d[key].(string); ok {
		return v
	}
	return ""
}

func getFloat(d map[string]interface{}, key string, def float64) float64 {
	switch v := d[key].(type) {
	case float64:
		return v
	case int:
		return float64(v)
	case int64:
		return float64(v)
	case int32:
		return float64(v)
	case string:
		var f float64
		if _, err := fmt.Sscanf(v, "%g", &f); err == nil {
			return f
		}
	}
	return def
}

func getInt(d map[string]interface{}, key string, def int) int {
	return int(getFloat(d, key, float64(def)))
}

func getMap(d map[string]interface{}, key string) map[string]interface{} {
	if v, ok := d[key].(map[string]interface{}); ok {
		return v
	}
	return nil
}

func getList(d map[string]interface{}, key string) []interface{} {
	if v, ok := d[key].([]interface{}); ok {
		return v
	}
	return nil
}

// hasNonNull — ключ есть и не null.
func hasNonNull(d map[string]interface{}, key string) bool {
	v, ok := d[key]
	return ok && v != nil
}

var logFileName = "overlay.log"

func SetLogFileName(name string) {
	if name != "" {
		logFileName = name
	}
}

// LogLine — одна строка в overlay.log (appDir), формат как в C#-версии.
func LogLine(appDir, msg string) {
	if appDir == "" {
		return
	}
	f, err := os.OpenFile(filepath.Join(appDir, logFileName), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	fmt.Fprintf(f, "[%s] %s\n", NowIso(), msg)
}
