package env

import (
	"bufio"
	"os"
	"strings"
)

// Parser читает .env (кавычки, inline-комментарии, export-префикс),
// с fallback на переменные окружения процесса.
type Parser struct {
	entries map[string]string
}

func NewParser(envPath string) *Parser {
	p := &Parser{entries: map[string]string{}}
	if envPath == "" {
		return p
	}
	f, err := os.Open(envPath)
	if err != nil {
		return p
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasPrefix(strings.ToLower(line), "export") {
			rest := line[6:]
			if len(rest) > 0 && (rest[0] == ' ' || rest[0] == '\t') {
				line = strings.TrimSpace(rest)
			}
		}
		eq := strings.Index(line, "=")
		if eq <= 0 {
			continue
		}
		key := strings.TrimSpace(line[:eq])
		if key == "" {
			continue
		}
		p.entries[strings.ToLower(key)] = ParseValue(strings.TrimSpace(line[eq+1:]))
	}
	return p
}

// Get — значение из .env, иначе из окружения процесса.
func (p *Parser) Get(key string) string {
	if v, ok := p.entries[strings.ToLower(key)]; ok && v != "" {
		return strings.TrimSpace(v)
	}
	if v := os.Getenv(key); v != "" {
		return strings.TrimSpace(v)
	}
	return ""
}

// ParseValue — парсинг значения: кавычки ('...'/'"..." с escapes), inline '#'-комментарий.
func ParseValue(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	if raw[0] == '"' || raw[0] == '\'' {
		q := raw[0]
		var sb strings.Builder
		closed := false
		for i := 1; i < len(raw); i++ {
			c := raw[i]
			if q == '"' && c == '\\' && i+1 < len(raw) {
				n := raw[i+1]
				switch n {
				case '"', '\\', '$', '`':
					sb.WriteByte(n)
					i++
					continue
				case 'n':
					sb.WriteByte('\n')
					i++
					continue
				case 'r':
					sb.WriteByte('\r')
					i++
					continue
				case 't':
					sb.WriteByte('\t')
					i++
					continue
				}
			}
			if c == q {
				closed = true
				break
			}
			sb.WriteByte(c)
		}
		if closed {
			return strings.TrimSpace(sb.String())
		}
		return strings.TrimSpace(raw[1:])
	}
	if i := strings.Index(raw, "#"); i >= 0 {
		raw = raw[:i]
	}
	return strings.TrimSpace(raw)
}

// IsPlaceholderKey — пусто, плейсхолдер из .env.example или явно не ключ.
func IsPlaceholderKey(value string) bool {
	v := strings.TrimSpace(value)
	v = strings.Trim(v, "\"'")
	v = strings.TrimSpace(v)
	if v == "" {
		return true
	}
	low := strings.ToLower(v)
	if strings.Contains(low, "...") || strings.Contains(low, "here") || strings.Contains(low, "xxxx") {
		return true
	}
	if low == "sk-or-v1-your-openrouter-key-here" || low == "sk-your-kimi-key-here" {
		return true
	}
	if strings.HasPrefix(low, "sk-or-v1-") && strings.HasSuffix(low, "-here") {
		return true
	}
	return false
}
