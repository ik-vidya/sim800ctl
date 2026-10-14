// Пакет number — нормализация телефонных номеров.
package number

import (
	"fmt"
	"strings"
)

// Config — параметры нормализации.
type Config struct {
	// CountryCode — код страны без знака «+» (например, "371").
	CountryCode string
	// LocalLength — длина локального номера без кода страны (например, 8).
	// 0 — отключить автоматическое добавление кода.
	LocalLength int
}

// Normalize приводит номер к международному формату.
//
// Правила:
//   - Удаляются пробелы, дефисы, скобки, точки, табы.
//   - Если номер начинается с "00" — двойной ноль заменяется на "+".
//   - Если номер не начинается с "+" и его длина равна LocalLength —
//     добавляется "+" и код страны.
//   - В остальных случаях номер возвращается как есть.
func Normalize(cfg Config, input string) string {
	n := stripSeparators(input)
	if n == "" {
		return ""
	}
	if strings.HasPrefix(n, "00") {
		return "+" + n[2:]
	}
	if !strings.HasPrefix(n, "+") {
		if cfg.LocalLength > 0 && len(n) == cfg.LocalLength {
			return "+" + cfg.CountryCode + n
		}
	}
	return n
}

// stripSeparators удаляет типичные разделители из номера.
func stripSeparators(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		switch r {
		case ' ', '-', '(', ')', '.', '\t', '\r', '\n':
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

// Validate проверяет, что номер пригоден для отправки.
func Validate(normalized string) error {
	if normalized == "" {
		return fmt.Errorf("номер пуст")
	}
	digits := normalized
	if strings.HasPrefix(digits, "+") {
		digits = digits[1:]
	}
	if len(digits) < 3 || len(digits) > 20 {
		return fmt.Errorf("некорректная длина номера: %d цифр (нужно 3–20)", len(digits))
	}
	for _, r := range digits {
		if r < '0' || r > '9' {
			return fmt.Errorf("номер содержит недопустимый символ %q", r)
		}
	}
	return nil
}
