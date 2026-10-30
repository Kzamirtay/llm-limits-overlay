package fetchers

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

const HttpTimeout = 3500 * time.Millisecond

var cyrillicRe = regexp.MustCompile(`[а-яА-Я]`)

// HttpGet — GET с Bearer и общим таймаутом 3.5с. Возвращает тело и заголовки.
func HttpGet(url, key string) (int, map[string][]string, []byte, error) {
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return 0, nil, nil, err
	}
	req.Header.Set("User-Agent", "LLM-Limits-Overlay/1.0")
	if key != "" {
		req.Header.Set("Authorization", "Bearer "+key)
	}
	client := &http.Client{Timeout: HttpTimeout}
	resp, err := client.Do(req)
	if err != nil {
		return 0, nil, nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return resp.StatusCode, resp.Header, nil, err
	}
	if resp.StatusCode/100 != 2 {
		return resp.StatusCode, resp.Header, body, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return resp.StatusCode, resp.Header, body, nil
}

// FormatTimeDiff — "3d 8h" / "2h 40m" / "45m" / "ready"; empty -> emptyVal.
func FormatTimeDiff(iso string, emptyVal string) string {
	if iso == "" {
		return emptyVal
	}
	t, err := parseIso(iso)
	if err != nil {
		return iso
	}
	sec := int(time.Until(t).Seconds())
	if sec <= 0 {
		return "ready"
	}
	d := sec / 86400
	h := (sec % 86400) / 3600
	m := (sec % 3600) / 60
	if d > 0 {
		return fmt.Sprintf("%dd %dh", d, h)
	}
	if h > 0 {
		return fmt.Sprintf("%dh %dm", h, m)
	}
	return fmt.Sprintf("%dm", m)
}

func parseIso(s string) (time.Time, error) {
	if t, err := time.Parse(time.RFC3339Nano, s); err == nil {
		return t, nil
	}
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t, nil
	}
	if t, err := time.Parse("2006-01-02 15:04:05", s); err == nil {
		return t, nil
	}
	return time.Time{}, fmt.Errorf("unparseable time: %s", s)
}

// NowIso — локальное время в формате конфига.
func NowIso() string {
	return time.Now().Format("2006-01-02 15:04:05")
}

// MaskOpenRouterLabel — не писать ключ в limits.json: маска first6...last4 или "unnamed".
func MaskOpenRouterLabel(label string) string {
	t := strings.TrimSpace(label)
	if t == "" {
		return ""
	}
	low := strings.ToLower(t)
	looksLikeKey := strings.HasPrefix(low, "sk-") ||
		(len(t) > 24 && !strings.Contains(t, " ") && !cyrillicRe.MatchString(t))
	if !looksLikeKey {
		return label
	}
	if len(t) <= 10 {
		return "unnamed"
	}
	return t[:6] + "..." + t[len(t)-4:]
}

// WriteAtomicJson — запись через tmp + rename.
func WriteAtomicJson(path string, v interface{}) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	f, err := os.Create(tmp)
	if err != nil {
		return err
	}
	enc, err2 := marshalPretty(v)
	if err2 == nil {
		_, err = f.Write(enc)
	}
	f.Close()
	if err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func GetUsedStatusLevel(usedFraction float64) string {
	if usedFraction >= 0.50 {
		return "critical"
	}
	if usedFraction >= 0.20 {
		return "warning"
	}
	return "ok"
}
