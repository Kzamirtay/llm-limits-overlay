package fetchers

import (
	"os"
	"runtime"
	"strconv"
	"strings"
)

// DefaultAgyTimeoutSec — платформенный дефолт таймаута запуска agy.
//
// Windows: 25 с. В 2.2.0 стояло 8 с, и этого перестало хватать, как только
// agy.exe подрос до ~230 МБ: холодный старт + запрос /usage занимает 6–8 с и
// больше. Процесс убивался по ctx, а таймаут закрывает latch (fail-closed,
// чтобы не плодить интерактивные OAuth-окна) — и карточка AGY замирала на
// прошлом успешном снимке до смены кредов или ручного «Обновить».
//
// Unix: 20 с — там тот же agy отвечает за 4–5 с, но запас нужен на обновления.
func DefaultAgyTimeoutSec() int {
	if runtime.GOOS == "windows" {
		return 25
	}
	return 20
}

// clampAgyTimeout — допустимый диапазон значений (как у --agy-timeout, 1..120).
// Всё вне диапазона считается «не задано» (0), а не ошибкой.
func clampAgyTimeout(n int) int {
	if n >= 1 && n <= 120 {
		return n
	}
	return 0
}

// AgyTimeoutFromEnv читает LLM_LIMITS_AGY_TIMEOUT (1..120); 0 = не задано/мусор.
func AgyTimeoutFromEnv() int {
	v := strings.TrimSpace(os.Getenv("LLM_LIMITS_AGY_TIMEOUT"))
	if v == "" {
		return 0
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return 0
	}
	return clampAgyTimeout(n)
}

// ResolveAgyTimeout — приоритет источников:
//
//	явный флаг/аргумент (flagSec) → LLM_LIMITS_AGY_TIMEOUT → overlay.config.json
//	("agyTimeout" = cfgSec) → платформенный дефолт.
//
// Невалидные значения молча пропускаются: конфиг и env не должны ломать запуск.
func ResolveAgyTimeout(flagSec, cfgSec int) int {
	if n := clampAgyTimeout(flagSec); n > 0 {
		return n
	}
	if n := AgyTimeoutFromEnv(); n > 0 {
		return n
	}
	if n := clampAgyTimeout(cfgSec); n > 0 {
		return n
	}
	return DefaultAgyTimeoutSec()
}
