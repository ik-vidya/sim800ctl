#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""
apply-sim800ctl-fixes.py

Фиксы по обратной связи от разработчика модема:

1. AT+COPS=? — таймаут 60s → 180s (test5). На SIM800 сканирование
   всех диапазонов занимает до 2-3 минут; 60s давало ложное
   «RF не работает» на исправном модеме.

2. readLine принимает \r как терминатор наравне с \n. SIM800 при
   сбое (переполнение приёмного буфера, +CME ERROR: SIM busy)
   присылает строки без \n. Раньше readLine возвращал "", а
   drain терял обрезанную строку (симптом "<< +CME ER" в логе).

3. drain дочитывает неполную строку до 200 мс перед сбросом.

4. Первые тесты в репозитории: internal/modem/modem_test.go
   (7 тестов на readLine + проверка TestDescription).

5. docs/SIM800L-NOTES.md: раздел 13, «60 секунд» → «180 секунд».

Запускать из корня sim800ctl.
"""
import sys
from pathlib import Path

ROOT = Path(__file__).parent


def write(path, content):
    p = ROOT / path
    p.parent.mkdir(parents=True, exist_ok=True)
    new = content.lstrip("\n")
    old = p.read_text(encoding="utf-8") if p.exists() else None
    if old == new:
        print(f"  = {path} (без изменений)")
        return
    p.write_text(new, encoding="utf-8")
    print(f"  ✓ {path}")


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


# ============================================================================
# internal/modem/modem.go — полная перезапись
# ============================================================================
MODEM_GO = '''\
// Пакет modem — обёртка над последовательным портом и AT-командами.
package modem

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"go.bug.st/serial"
)

// Длительности по умолчанию.
const (
	syncAttempts = 5
	syncTimeout  = 1 * time.Second
	promptWait   = 10 * time.Second
	finalWait    = 60 * time.Second
)

// Modem — открытый порт с модемом.
type Modem struct {
	port    serial.Port
	mu      sync.Mutex
	buf     []byte
	verbose bool
	log     func(string)
}

// New открывает порт и возвращает готовый Modem.
// log — функция логирования (может быть nil).
// verbose — печатать ли AT-команды и ответы.
func New(portName string, baud int, verbose bool, log func(string)) (*Modem, error) {
	mode := &serial.Mode{
		BaudRate: baud,
		DataBits: 8,
		Parity:   serial.NoParity,
		StopBits: serial.OneStopBit,
	}
	p, err := serial.Open(portName, mode)
	if err != nil {
		return nil, fmt.Errorf("не удалось открыть порт %s: %w", portName, err)
	}
	_ = p.SetReadTimeout(100 * time.Millisecond)

	if log == nil {
		log = func(string) {}
	}
	return &Modem{port: p, verbose: verbose, log: log}, nil
}

// Close закрывает порт.
func (m *Modem) Close() {
	if m.port != nil {
		_ = m.port.Close()
	}
}

// SetVerbose включает/выключает печать AT-команд.
func (m *Modem) SetVerbose(v bool) { m.verbose = v }

// debug печатает строку, если verbose.
func (m *Modem) debug(format string, args ...any) {
	if m.verbose {
		m.log(fmt.Sprintf(format, args...))
	}
}

// readAvailable читает доступные байты в буфер.
func (m *Modem) readAvailable() {
	buf := make([]byte, 4096)
	n, err := m.port.Read(buf)
	if err == nil && n > 0 {
		m.buf = append(m.buf, buf[:n]...)
	}
}

// readLine возвращает следующую непустую строку из буфера.
//
// Терминатором считается и \\r, и \\n: SIM800 (и другие модемы)
// при переполнении приёмного буфера или при сбое SIM иногда
// присылают строки без \\n (только \\r). Без этого readLine
// возвращал бы "" до прихода следующей команды, а drain терял
// бы обрезанную строку (симптом: "<< +CME ER" в логе вместо
// "+CME ERROR: SIM busy").
//
// При \\r\\n считаем это одним терминатором: \\n после \\r
// пропускается, пустая строка не появляется.
func (m *Modem) readLine() string {
	for {
		idx := -1
		var term byte
		for i, b := range m.buf {
			if b == '\\r' || b == '\\n' {
				idx = i
				term = b
				break
			}
		}
		if idx < 0 {
			return ""
		}
		line := strings.TrimSpace(string(m.buf[:idx]))
		next := idx + 1
		if next < len(m.buf) {
			// \\r\\n или \\n\\r — считаем одним терминатором.
			if (term == '\\r' && m.buf[next] == '\\n') ||
				(term == '\\n' && m.buf[next] == '\\r') {
				next++
			}
		}
		m.buf = m.buf[next:]
		if line != "" {
			return line
		}
	}
}

// hasTerminator сообщает, есть ли в буфере \\r или \\n.
func hasTerminator(b []byte) bool {
	for _, c := range b {
		if c == '\\r' || c == '\\n' {
			return true
		}
	}
	return false
}

// isError — строка является ошибкой?
func isError(line string) bool {
	return line == "ERROR" ||
		strings.HasPrefix(line, "+CME ERROR") ||
		strings.HasPrefix(line, "+CMS ERROR")
}

// Sync синхронизирует скорость с модемом (autobauding).
func (m *Modem) Sync() error {
	var lastErr error
	for i := 0; i < syncAttempts; i++ {
		_, err := m.Command("AT", syncTimeout)
		if err == nil {
			return nil
		}
		lastErr = err
	}
	if lastErr == nil {
		lastErr = errors.New("нет ответа")
	}
	return fmt.Errorf("модем не отвечает на AT (%w). "+
		"Проверьте питание, соединение TX/RX и скорость порта", lastErr)
}

// Command отправляет AT-команду и ждёт OK/ERROR.
// Возвращает накопленные строки ответа (без OK) или ошибку.
func (m *Modem) Command(cmd string, timeout time.Duration) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.drain()
	m.debug(">> %s", cmd)
	if _, err := m.port.Write([]byte(cmd + "\\r")); err != nil {
		return "", fmt.Errorf("запись в порт: %w", err)
	}
	return m.waitFinal(cmd, timeout)
}

// SendPdu отправляет PDU через AT+CMGS.
// Возвращает строку ответа модема (обычно содержит +CMGS: <ref>).
func (m *Modem) SendPdu(hexPdu string, tpduLength int) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.drain()
	cmd := fmt.Sprintf("AT+CMGS=%d", tpduLength)
	m.debug(">> %s", cmd)
	if _, err := m.port.Write([]byte(cmd + "\\r")); err != nil {
		return "", fmt.Errorf("запись AT+CMGS: %w", err)
	}
	if err := m.waitPrompt(promptWait); err != nil {
		// Отмена висящей CMGS.
		_, _ = m.port.Write([]byte{0x1B})
		return "", err
	}
	m.debug(">> %s <Ctrl+Z>", hexPdu)
	if _, err := m.port.Write([]byte(hexPdu + "\\x1A")); err != nil {
		return "", fmt.Errorf("запись PDU: %w", err)
	}
	return m.waitFinal("", finalWait)
}

// drain читает и логирует все доступные строки, сбрасывает буфер.
// Вызывается перед отправкой команды, чтобы не получить остатки
// прошлых ответов.
//
// Если в буфере осталась неполная строка (нет \\r и \\n) —
// дочитываем до 200 мс: модем мог ещё не дописать её.
// Если и через 200 мс терминатор не появился — выводим остаток
// как "(incomplete)" и сбрасываем, чтобы обрезанная строка не
// склеилась с ответом следующей команды.
func (m *Modem) drain() {
	m.readAvailable()
	for {
		line := m.readLine()
		if line == "" {
			break
		}
		m.debug("<< %s", line)
	}

	if len(m.buf) == 0 {
		return
	}

	// В буфере неполная строка — дочитываем.
	deadline := time.Now().Add(200 * time.Millisecond)
	for !hasTerminator(m.buf) && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
		m.readAvailable()
	}

	for {
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
}

// waitFinal ждёт OK или ERROR до таймаута.
func (m *Modem) waitFinal(echo string, timeout time.Duration) (string, error) {
	deadline := time.Now().Add(timeout)
	var response []string
	for time.Now().Before(deadline) {
		m.readAvailable()
		for {
			line := m.readLine()
			if line == "" {
				break
			}
			if line == echo {
				continue
			}
			m.debug("<< %s", line)
			if line == "OK" {
				return strings.Join(response, "\\n"), nil
			}
			if isError(line) {
				return "", errors.New(InterpretErrorLine(line))
			}
			response = append(response, line)
		}
		time.Sleep(20 * time.Millisecond)
	}
	return "", fmt.Errorf("таймаут ожидания ответа (%s)", timeout)
}

// waitPrompt ждёт приглашение "> " для ввода PDU.
func (m *Modem) waitPrompt(timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		m.readAvailable()
		if idx := indexByte(m.buf, '>'); idx >= 0 {
			m.buf = m.buf[idx+1:]
			m.debug("<< >")
			return nil
		}
		for {
			line := m.readLine()
			if line == "" {
				break
			}
			m.debug("<< %s", line)
			if isError(line) {
				return errors.New(line)
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	return errors.New("таймаут ожидания приглашения '>'")
}

// indexByte — обёртка для поиска байта.
func indexByte(b []byte, c byte) int {
	for i, x := range b {
		if x == c {
			return i
		}
	}
	return -1
}
'''


# ============================================================================
# internal/modem/tests.go — точечные правки
# ============================================================================
TESTS_PATCHES = [
    (
        'return "ИЗМЕРЕНИЕ: сканирование сетей (AT+COPS=?, до 60 с)"',
        'return "ИЗМЕРЕНИЕ: сканирование сетей (AT+COPS=?, до 180 с)"',
    ),
    (
        'm.log("Длительность: до 60 секунд активного сканирования.")',
        'm.log("Длительность: до 180 секунд активного сканирования.")',
    ),
    (
        'resp, err := m.Command("AT+COPS=?", 60*time.Second)',
        'resp, err := m.Command("AT+COPS=?", 180*time.Second)',
    ),
]


# ============================================================================
# docs/SIM800L-NOTES.md — точечные правки (раздел 13)
# ============================================================================
NOTES_PATCHES = [
    (
        "**Проблема.** `AT+COPS=?` может занимать до 60 секунд. Модем",
        "**Проблема.** `AT+COPS=?` может занимать до 180 секунд. Модем",
    ),
    (
        "**Обход.** Держать таймаут команды >= 60 секунд. В `sim800ctl`",
        "**Обход.** Держать таймаут команды >= 180 секунд. В `sim800ctl`",
    ),
]


# ============================================================================
# internal/modem/modem_test.go — новый
# ============================================================================
MODEM_TEST_GO = '''\
package modem

import (
	"strings"
	"testing"
)

// ==================== readLine: терминаторы ====================
//
// readLine должен принимать и \\r, и \\n как терминатор строки.
// SIM800 при сбое (переполнение приёмного буфера, +CME ERROR:
// SIM busy) шлёт строки без \\n. Раньше readLine искал только \\n,
// возвращал "", а drain терял обрезанную строку (симптом
// "<< +CME ER" в логе).

func TestReadLineLF(t *testing.T) {
	m := &Modem{buf: []byte("OK\\n")}
	if got := m.readLine(); got != "OK" {
		t.Errorf("readLine=%q, ожидалось %q", got, "OK")
	}
}

func TestReadLineCRLF(t *testing.T) {
	m := &Modem{buf: []byte("OK\\r\\n")}
	if got := m.readLine(); got != "OK" {
		t.Errorf("readLine=%q, ожидалось %q", got, "OK")
	}
}

func TestReadLineBareCR(t *testing.T) {
	// Ключевой кейс из реального лога: SIM800 прислал ответ
	// без \\n. Раньше строка терялась.
	m := &Modem{buf: []byte("+CME ERROR: SIM busy\\r")}
	if got := m.readLine(); got != "+CME ERROR: SIM busy" {
		t.Errorf("readLine=%q, ожидалось %q",
			got, "+CME ERROR: SIM busy")
	}
}

func TestReadLineIncomplete(t *testing.T) {
	// Нет терминатора — пустая строка, буфер не тронут.
	m := &Modem{buf: []byte("+CME ER")}
	if got := m.readLine(); got != "" {
		t.Errorf("readLine=%q, ожидалось пусто", got)
	}
	if string(m.buf) != "+CME ER" {
		t.Errorf("буфер изменён: %q", m.buf)
	}
}

func TestReadLineSkipEmpty(t *testing.T) {
	// Пустые строки (после \\r\\n) пропускаются, не возвращаются.
	m := &Modem{buf: []byte("\\r\\n\\r\\nOK\\r\\n")}
	if got := m.readLine(); got != "OK" {
		t.Errorf("readLine=%q, ожидалось %q", got, "OK")
	}
	if len(m.buf) != 0 {
		t.Errorf("буфер не пуст: %q", m.buf)
	}
}

func TestReadLineMultiple(t *testing.T) {
	m := &Modem{buf: []byte("+CSQ: 17,0\\r\\nOK\\r\\n")}
	got1 := m.readLine()
	got2 := m.readLine()
	if got1 != "+CSQ: 17,0" || got2 != "OK" {
		t.Errorf("readLine #1=%q, #2=%q", got1, got2)
	}
}

func TestReadLineConsecutiveCR(t *testing.T) {
	// Две строки через \\r без \\n — обе должны вернуться.
	m := &Modem{buf: []byte("+CMTI: \\"SM\\",5\\r+CSQ: 17,0\\r")}
	got1 := m.readLine()
	got2 := m.readLine()
	if got1 != `+CMTI: "SM",5` {
		t.Errorf("readLine #1=%q", got1)
	}
	if got2 != "+CSQ: 17,0" {
		t.Errorf("readLine #2=%q", got2)
	}
}

// ==================== TestDescription ====================

func TestTestDescriptionTest5Has180(t *testing.T) {
	// Регрессионный тест: раньше было «до 60 с». Разработчик
	// модема указал, что AT+COPS=? на SIM800 занимает до 2-3
	// минут; тест5 должен упоминать 180.
	d := TestDescription("test5")
	if !strings.Contains(d, "180") {
		t.Errorf("TestDescription(\\"test5\\")=%q, ожидалось упоминание 180",
			d)
	}
	if strings.Contains(d, "60") {
		t.Errorf("TestDescription(\\"test5\\") всё ещё содержит 60: %q", d)
	}
}
'''


def main():
    if not (ROOT / "go.mod").exists():
        print("ошибка: запускайте из корня sim800ctl (где go.mod)",
              file=sys.stderr)
        sys.exit(1)

    # Проверим, что это именно sim800ctl, а не smsgateway.
    gm = (ROOT / "go.mod").read_text(encoding="utf-8")
    if "sim800ctl" not in gm:
        print("ошибка: это не sim800ctl (go.mod не содержит sim800ctl)",
              file=sys.stderr)
        sys.exit(1)

    print("Перезаписываю internal/modem/modem.go "
          "(readLine с \\r, drain с дочитыванием):")
    write("internal/modem/modem.go", MODEM_GO)

    print()
    print("Патчу internal/modem/tests.go (таймаут COPS=? = 180s):")
    patch("internal/modem/tests.go", TESTS_PATCHES)

    print()
    print("Патчу docs/SIM800L-NOTES.md (раздел 13):")
    patch("docs/SIM800L-NOTES.md", NOTES_PATCHES)

    print()
    print("Создаю internal/modem/modem_test.go (первые тесты в репо):")
    write("internal/modem/modem_test.go", MODEM_TEST_GO)

    print()
    print("Готово. Проверки:")
    print("    gofmt -w internal/")
    print("    GOWORK=off go mod tidy")
    print("    GOWORK=off go build ./...")
    print("    GOWORK=off go vet ./...")
    print("    GOWORK=off go test ./internal/modem/ -v -timeout 60s")
    print("    GOWORK=off go test ./... -timeout 60s")


if __name__ == "__main__":
    main()