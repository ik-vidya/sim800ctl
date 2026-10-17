#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""
fix-log-sanitize.py

Регрессия после readLine с \\r: шум на линии (high-bit байты,
невалидный UTF-8) теперь виден в логе как `<< �`. Раньше такие
байты молча терялись (drain обнулял буфер), поэтому в логе их
не было.

Фикс — санитизация при логировании:
  - байты вне ASCII 0x20–0x7E печатаются как \\xNN;
  - строки, состоящие ТОЛЬКО из такого шума, пропускаются
    (не пишем `<< ` с пустотой).

Это НЕ меняет readLine и парсинг — только формат вывода.

Патчит:
  internal/modem/modem.go
Добавляет:
  internal/modem/modem_test.go — тесты sanitizeForLog/hasPrintable
"""
import sys
from pathlib import Path

ROOT = Path(__file__).parent


def patch(path, replacements):
    p = ROOT / path
    src = p.read_text(encoding="utf-8")
    orig = src
    for old, new in replacements:
        if old not in src:
            if new in src:
                continue
            raise RuntimeError(f"{path}: не найден якорь:\n{old[:200]}")
        src = src.replace(old, new, 1)
    if src == orig:
        print(f"  = {path} (без изменений)")
        return
    p.write_text(src, encoding="utf-8")
    print(f"  ✓ {path} (patched)")


def append_if_missing(path, marker, content):
    p = ROOT / path
    src = p.read_text(encoding="utf-8")
    if marker in src:
        print(f"  = {path} (уже есть {marker!r})")
        return
    p.write_text(src.rstrip() + "\n\n" + content, encoding="utf-8")
    print(f"  ✓ {path} (тесты добавлены)")


# ============================================================================
# modem.go — санитизация при логировании
# ============================================================================
MODEM_PATCHES = [
    # 1. Добавить sanitizeForLog и hasPrintable, метод logLine.
    #    Вставим после debug().
    (
'''// debug печатает строку, если verbose.
func (m *Modem) debug(format string, args ...any) {
	if m.verbose {
		m.log(fmt.Sprintf(format, args...))
	}
}''',
'''// debug печатает строку, если verbose.
func (m *Modem) debug(format string, args ...any) {
	if m.verbose {
		m.log(fmt.Sprintf(format, args...))
	}
}

// logLine печатает строку, полученную из порта, в debug-лог.
//
// Байты вне ASCII 0x20–0x7E (шум на линии, invalid UTF-8,
// высокобитные байты) печатаются как \\xNN — иначе терминал
// рисует `�` и лог становится нечитаемым.
//
// Если после фильтрации в строке нет ни одного печатного
// символа — она не печатается совсем (это чистый шум).
func (m *Modem) logLine(line string) {
	if !hasPrintable(line) {
		return
	}
	m.debug("<< %s", sanitizeForLog(line))
}

// sanitizeForLog заменяет непечатаемые байты на \\xNN.
// Печатные ASCII-символы остаются как есть.
func sanitizeForLog(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c >= 0x20 && c < 0x7F {
			b.WriteByte(c)
		} else {
			fmt.Fprintf(&b, "\\\\x%02x", c)
		}
	}
	return b.String()
}

// hasPrintable сообщает, есть ли в строке хотя бы один
// печатный ASCII-символ.
func hasPrintable(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] >= 0x20 && s[i] < 0x7F {
			return true
		}
	}
	return false
}'''),

    # 2. drain: << %s → logLine.
    (
'''	m.readAvailable()
	for {
		line := m.readLine()
		if line == "" {
			break
		}
		m.debug("<< %s", line)
	}

	if len(m.buf) == 0 {
		return
	}''',
'''	m.readAvailable()
	for {
		line := m.readLine()
		if line == "" {
			break
		}
		m.logLine(line)
	}

	if len(m.buf) == 0 {
		return
	}'''),

    # 3. drain: incomplete — тоже санитизация.
    (
'''	for {
		line := m.readLine()
		if line == "" {
			break
		}
		m.debug("<< %s", line)
	}
	if len(m.buf) > 0 {
		m.debug("<< (incomplete) %q", string(m.buf))
		m.buf = nil
	}
}''',
'''	for {
		line := m.readLine()
		if line == "" {
			break
		}
		m.logLine(line)
	}
	if len(m.buf) > 0 {
		if hasPrintable(string(m.buf)) {
			m.debug("<< (incomplete) %s",
				sanitizeForLog(string(m.buf)))
		}
		m.buf = nil
	}
}'''),

    # 4. waitFinal: << %s → logLine.
    (
'''			if line == echo {
				continue
			}
			m.debug("<< %s", line)
			if line == "OK" {
				return strings.Join(response, "\\n"), nil
			}''',
'''			if line == echo {
				continue
			}
			m.logLine(line)
			if line == "OK" {
				return strings.Join(response, "\\n"), nil
			}'''),

    # 5. waitPrompt: << %s → logLine.
    (
'''		for {
			line := m.readLine()
			if line == "" {
				break
			}
			m.debug("<< %s", line)
			if isError(line) {
				return errors.New(line)
			}
		}''',
'''		for {
			line := m.readLine()
			if line == "" {
				break
			}
			m.logLine(line)
			if isError(line) {
				return errors.New(line)
			}
		}'''),
]


# ============================================================================
# modem_test.go — добавить тесты
# ============================================================================
NEW_TESTS = '''\
// ==================== sanitizeForLog / hasPrintable ====================
//
// Шум на линии (high-bit байты, invalid UTF-8) раньше молча
// терялся в drain (m.buf = nil без логирования). После фикса
// readLine с \\r такие байты стали видны в логе как `<< �`.
// Теперь они печатаются как \\xNN, а строки из чистого шума
// пропускаются.

func TestSanitizeForLog(t *testing.T) {
	cases := map[string]string{
		"OK":                    "OK",
		"+CSQ: 17,0":            "+CSQ: 17,0",
		"\\x80OK":                "\\\\x80OK",
		"\\x00\\x01\\x02":            "\\\\x00\\\\x01\\\\x02",
		"a\\x80b":                "a\\\\x80b",
		"+CMTI: \\"SM\\",5\\x7f":     "+CMTI: \\"SM\\",5\\\\x7f",
		"":                      "",
		"\\xff\\xfe":              "\\\\xff\\\\xfe",
	}
	for in, want := range cases {
		if got := sanitizeForLog(in); got != want {
			t.Errorf("sanitizeForLog(%q)=%q, ожидалось %q",
				in, got, want)
		}
	}
}

func TestHasPrintable(t *testing.T) {
	cases := map[string]bool{
		"OK":         true,
		"+CSQ: 17,0": true,
		"\\x80OK":     true, // есть OK
		"\\x80":       false,
		"\\x00\\x01":   false,
		"":           false,
		" ":          true, // пробел — печатный (0x20)
	}
	for in, want := range cases {
		if got := hasPrintable(in); got != want {
			t.Errorf("hasPrintable(%q)=%v, ожидалось %v",
				in, got, want)
		}
	}
}

func TestLogLineFiltersGarbage(t *testing.T) {
	var logged []string
	m := &Modem{
		verbose: true,
		log:     func(s string) { logged = append(logged, s) },
	}

	m.logLine("\\x80")     // чистый шум — пропустить
	m.logLine("OK")        // печатается как есть
	m.logLine("\\x80OK")   // печатается с \\x80
	m.logLine("")          // пусто — пропустить

	if len(logged) != 2 {
		t.Fatalf("len(logged)=%d, ожидалось 2: %v", len(logged), logged)
	}
	if logged[0] != "<< OK" {
		t.Errorf("logged[0]=%q", logged[0])
	}
	if logged[1] != "<< \\\\x80OK" {
		t.Errorf("logged[1]=%q", logged[1])
	}
}
'''


def main():
    if not (ROOT / "go.mod").exists():
        print("ошибка: запускайте из корня sim800ctl", file=sys.stderr)
        sys.exit(1)

    gm = (ROOT / "go.mod").read_text(encoding="utf-8")
    if "sim800ctl" not in gm:
        print("ошибка: это не sim800ctl", file=sys.stderr)
        sys.exit(1)

    print("Патчу internal/modem/modem.go:")
    patch("internal/modem/modem.go", MODEM_PATCHES)

    print()
    print("Добавляю тесты в internal/modem/modem_test.go:")
    append_if_missing(
        "internal/modem/modem_test.go",
        "func TestSanitizeForLog",
        NEW_TESTS,
    )

    print()
    print("Готово. Проверки:")
    print("    gofmt -w internal/")
    print("    GOWORK=off go build ./...")
    print("    GOWORK=off go vet ./...")
    print("    GOWORK=off go test ./internal/modem/ -v -timeout 30s")
    print("    GOWORK=off go test ./... -timeout 30s")


if __name__ == "__main__":
    main()