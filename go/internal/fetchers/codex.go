package fetchers

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"llmlimits/internal/config"
	"llmlimits/internal/model"
)

// resolveCodexBin находит путь к бинарнику codex.
// Проверяет LLM_LIMITS_CODEX_BIN, PATH, ~/.local/node/bin/codex, ~/.local/node-v*/bin/codex.
func resolveCodexBin() string {
	if custom := os.Getenv("LLM_LIMITS_CODEX_BIN"); custom != "" {
		return custom
	}
	if p, err := exec.LookPath("codex"); err == nil {
		return p
	}
	if p, err := exec.LookPath("codex.cmd"); err == nil {
		return p
	}
	if p, err := exec.LookPath("codex.exe"); err == nil {
		return p
	}

	if home, err := os.UserHomeDir(); err == nil && home != "" {
		candidates := []string{
			filepath.Join(home, ".local", "node", "bin", "codex"),
			filepath.Join(home, ".local", "node", "bin", "codex.exe"),
		}
		globs, _ := filepath.Glob(filepath.Join(home, ".local", "node-v*", "bin", "codex"))
		candidates = append(candidates, globs...)
		for _, c := range candidates {
			if fi, err := os.Stat(c); err == nil && !fi.IsDir() {
				return c
			}
		}
	}
	return ""
}

// formatWindowName форматирует имя окна по windowDurationMins:
// 300 -> "5ч", 10080 -> "7д", 43200 -> "30д", иначе "Nч" / "Nд" / "Nм".
func formatWindowName(mins int) string {
	switch mins {
	case 300:
		return "5ч"
	case 10080:
		return "7д"
	case 43200:
		return "30д"
	default:
		if mins >= 1440 && mins%1440 == 0 {
			return fmt.Sprintf("%dд", mins/1440)
		}
		if mins >= 60 && mins%60 == 0 {
			return fmt.Sprintf("%dч", mins/60)
		}
		return fmt.Sprintf("%dм", mins)
	}
}

// FormatUnixDiff форматирует разницу от текущего времени до unix timestamp (сек):
// "3d 8h" / "2h 40m" / "45m" / "ready"; <=0 -> emptyVal.
func FormatUnixDiff(ts int64, emptyVal string) string {
	if ts <= 0 {
		return emptyVal
	}
	t := time.Unix(ts, 0)
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

// FetchCodex опрашивает локальный codex app-server по JSON-RPC stdio.
func FetchCodex(appDir string, p config.Provider) map[string]interface{} {
	id := p.Id
	if id == "" {
		id = "codex"
	}
	nowIso := NowIso()

	codexBin := resolveCodexBin()
	if codexBin == "" {
		LogLine(appDir, "["+id+"] codex binary not found")
		return saveFragment(appDir, id, nowIso, defaultCodex("error", "codex binary not found"))
	}
	if _, err := os.Stat(codexBin); err != nil {
		if _, lErr := exec.LookPath(codexBin); lErr != nil {
			LogLine(appDir, "["+id+"] codex binary not found: "+codexBin)
			return saveFragment(appDir, id, nowIso, defaultCodex("error", "codex binary not found"))
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, codexBin, "app-server")
	hideConsole(cmd)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		LogLine(appDir, "["+id+"] stdin pipe error: "+err.Error())
		return saveFragment(appDir, id, nowIso, defaultCodex("error", err.Error()))
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		LogLine(appDir, "["+id+"] stdout pipe error: "+err.Error())
		return saveFragment(appDir, id, nowIso, defaultCodex("error", err.Error()))
	}
	devNull, _ := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	if devNull != nil {
		defer devNull.Close()
		cmd.Stderr = devNull
	}

	if err := cmd.Start(); err != nil {
		LogLine(appDir, "["+id+"] start error: "+err.Error())
		return saveFragment(appDir, id, nowIso, defaultCodex("error", err.Error()))
	}

	// Гарантируем завершение процесса и закрытие дескрипторов
	defer func() {
		_ = stdin.Close()
		_ = stdout.Close()
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
		_ = cmd.Wait()
	}()

	// Отправляем initialize и account/rateLimits/read
	req1 := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"clientInfo":{"name":"llm-limits","version":"0.1"},"capabilities":{"experimentalApi":true}}}` + "\n"
	req2 := `{"jsonrpc":"2.0","id":2,"method":"account/rateLimits/read","params":{}}` + "\n"

	if _, err := io.WriteString(stdin, req1+req2); err != nil {
		LogLine(appDir, "["+id+"] write error: "+err.Error())
		return saveFragment(appDir, id, nowIso, defaultCodex("error", "write error: "+err.Error()))
	}

	scanner := bufio.NewScanner(stdout)
	buf := make([]byte, 1024*1024)
	scanner.Buffer(buf, 1024*1024)

	var respLine []byte
	for scanner.Scan() {
		line := scanner.Bytes()
		if bytes.Contains(line, []byte(`"id":2`)) || bytes.Contains(line, []byte(`"id": 2`)) {
			respLine = append([]byte(nil), line...)
			break
		}
	}

	if len(respLine) == 0 {
		errMsg := "no rateLimits response received from app-server"
		if ctx.Err() == context.DeadlineExceeded {
			errMsg = "codex app-server timed out (10s)"
		} else if err := scanner.Err(); err != nil {
			errMsg = "scanner error: " + err.Error()
		}
		LogLine(appDir, "["+id+"] "+errMsg)
		return saveFragment(appDir, id, nowIso, defaultCodex("error", errMsg))
	}

	var rpcResp struct {
		Id     int `json:"id"`
		Error  *struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
		Result json.RawMessage `json:"result"`
	}

	if err := json.Unmarshal(respLine, &rpcResp); err != nil {
		LogLine(appDir, "["+id+"] unmarshal error: "+err.Error())
		return saveFragment(appDir, id, nowIso, defaultCodex("error", "json error: "+err.Error()))
	}

	if rpcResp.Error != nil {
		LogLine(appDir, "["+id+"] rpc error: "+rpcResp.Error.Message)
		return saveFragment(appDir, id, nowIso, defaultCodex("error", rpcResp.Error.Message))
	}

	var readResult struct {
		OrdinaryUsageAllowed bool `json:"ordinaryUsageAllowed"`
		RateLimits           struct {
			LimitId string `json:"limitId"`
			Primary struct {
				UsedPercent        float64 `json:"usedPercent"`
				WindowDurationMins int     `json:"windowDurationMins"`
				ResetsAt           int64   `json:"resetsAt"`
			} `json:"primary"`
			Secondary *struct {
				UsedPercent        float64 `json:"usedPercent"`
				WindowDurationMins int     `json:"windowDurationMins"`
				ResetsAt           int64   `json:"resetsAt"`
			} `json:"secondary"`
			Credits struct {
				HasCredits bool        `json:"hasCredits"`
				Unlimited  bool        `json:"unlimited"`
				Balance    interface{} `json:"balance"`
			} `json:"credits"`
			PlanType string `json:"planType"`
		} `json:"rateLimits"`
	}

	if err := json.Unmarshal(rpcResp.Result, &readResult); err != nil {
		LogLine(appDir, "["+id+"] unmarshal result error: "+err.Error())
		return saveFragment(appDir, id, nowIso, defaultCodex("error", "result error: "+err.Error()))
	}

	rl := readResult.RateLimits
	planType := strings.TrimSpace(rl.PlanType)

	balanceStr := ""
	if rl.Credits.Balance != nil {
		switch v := rl.Credits.Balance.(type) {
		case string:
			balanceStr = strings.TrimSpace(v)
		case float64:
			if v > 0 {
				balanceStr = fmt.Sprintf("$%.2f", v)
			} else {
				balanceStr = "$0"
			}
		}
	}

	primPct := round1(clamp01(rl.Primary.UsedPercent/100.0) * 100)
	primFrac := round4(clamp01(rl.Primary.UsedPercent / 100.0))
	primReset := FormatUnixDiff(rl.Primary.ResetsAt, "--")
	primName := formatWindowName(rl.Primary.WindowDurationMins)

	primaryMap := map[string]interface{}{
		"name":          primName,
		"window_mins":   rl.Primary.WindowDurationMins,
		"used_percent":  primPct,
		"used_fraction": primFrac,
		"used_ratio":    primFrac,
		"reset_in":      primReset,
		"resets_at":     rl.Primary.ResetsAt,
	}

	res := map[string]interface{}{
		"status":       "online",
		"last_updated": nowIso,
		"plan_type":    planType,
		"balance":      balanceStr,
		"primary":      primaryMap,
	}

	if rl.Secondary != nil {
		secPct := round1(clamp01(rl.Secondary.UsedPercent/100.0) * 100)
		secFrac := round4(clamp01(rl.Secondary.UsedPercent / 100.0))
		secReset := FormatUnixDiff(rl.Secondary.ResetsAt, "--")
		secName := formatWindowName(rl.Secondary.WindowDurationMins)

		res["secondary"] = map[string]interface{}{
			"name":          secName,
			"window_mins":   rl.Secondary.WindowDurationMins,
			"used_percent":  secPct,
			"used_fraction": secFrac,
			"used_ratio":    secFrac,
			"reset_in":      secReset,
			"resets_at":     rl.Secondary.ResetsAt,
		}
	}

	return saveFragment(appDir, id, nowIso, res)
}

func defaultCodex(status, errMsg string) map[string]interface{} {
	m := map[string]interface{}{
		"status":       status,
		"last_updated": "",
		"plan_type":    "",
		"balance":      "",
		"primary": map[string]interface{}{
			"name":          "30д",
			"window_mins":   43200,
			"used_percent":  0.0,
			"used_fraction": 0.0,
			"used_ratio":    0.0,
			"reset_in":      "--",
			"resets_at":     int64(0),
		},
	}
	if errMsg != "" {
		m["error"] = errMsg
	}
	return m
}

// CodexToState преобразует сохранённый блок провайдера в model.CodexInfo.
func CodexToState(block map[string]interface{}) *model.CodexInfo {
	if block == nil {
		return nil
	}
	c := &model.CodexInfo{
		Status:      getStr(block, "status"),
		LastUpdated: getStr(block, "last_updated"),
		PlanType:    getStr(block, "plan_type"),
		Balance:     getStr(block, "balance"),
	}
	if prim := getMap(block, "primary"); prim != nil {
		c.PrimaryName = getStr(prim, "name")
		c.PrimaryMin = getInt(prim, "window_mins", 0)
		c.Primary = bucketFromMap(prim)
	}
	if sec := getMap(block, "secondary"); sec != nil {
		c.SecondaryName = getStr(sec, "name")
		c.SecondaryMin = getInt(sec, "window_mins", 0)
		b := bucketFromMap(sec)
		c.Secondary = &b
	}
	return c
}
