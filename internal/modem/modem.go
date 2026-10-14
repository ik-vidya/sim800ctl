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
func (m *Modem) readLine() string {
	for {
		idx := -1
		for i, b := range m.buf {
			if b == '\n' {
				idx = i
				break
			}
		}
		if idx < 0 {
			return ""
		}
		line := strings.TrimSpace(string(m.buf[:idx]))
		m.buf = m.buf[idx+1:]
		if line != "" {
			return line
		}
	}
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
	if _, err := m.port.Write([]byte(cmd + "\r")); err != nil {
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
	if _, err := m.port.Write([]byte(cmd + "\r")); err != nil {
		return "", fmt.Errorf("запись AT+CMGS: %w", err)
	}
	if err := m.waitPrompt(promptWait); err != nil {
		// Отмена висящей CMGS.
		_, _ = m.port.Write([]byte{0x1B})
		return "", err
	}
	m.debug(">> %s <Ctrl+Z>", hexPdu)
	if _, err := m.port.Write([]byte(hexPdu + "\x1A")); err != nil {
		return "", fmt.Errorf("запись PDU: %w", err)
	}
	return m.waitFinal("", finalWait)
}

// drain читает и логирует все доступные строки, сбрасывает буфер.
// Вызывается перед отправкой команды, чтобы не получить остатки
// прошлых ответов.
func (m *Modem) drain() {
	m.readAvailable()
	for {
		line := m.readLine()
		if line == "" {
			break
		}
		m.debug("<< %s", line)
	}
	if len(m.buf) > 0 {
		m.debug("<< %s", strings.TrimSpace(string(m.buf)))
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
				return strings.Join(response, "\n"), nil
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
