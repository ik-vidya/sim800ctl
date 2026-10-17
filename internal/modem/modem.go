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
//
// Ошибку Read логируем через m.log (не debug): если USB-порт
// отвалился во время waitFinal, без этого сообщения программа
// висит до таймаута без единой подсказки, что не так.
//
// При SetReadTimeout(100ms) go.bug.st/serial возвращает
// (0, nil) при обычном «нет данных» — это не ошибка и не
// логируется.
func (m *Modem) readAvailable() {
	buf := make([]byte, 4096)
	n, err := m.port.Read(buf)
	if err != nil {
		m.log(fmt.Sprintf("!! read error: %v", err))
		return
	}
	if n > 0 {
		m.buf = append(m.buf, buf[:n]...)
	}
}

// readLine возвращает следующую непустую строку из буфера.
//
// Терминатором считается и \r, и \n: SIM800 (и другие модемы)
// при переполнении приёмного буфера или при сбое SIM иногда
// присылают строки без \n (только \r). Без этого readLine
// возвращал бы "" до прихода следующей команды, а drain терял
// бы обрезанную строку (симптом: "<< +CME ER" в логе вместо
// "+CME ERROR: SIM busy").
//
// При \r\n считаем это одним терминатором: \n после \r
// пропускается, пустая строка не появляется.
func (m *Modem) readLine() string {
	for {
		idx := -1
		var term byte
		for i, b := range m.buf {
			if b == '\r' || b == '\n' {
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
			// \r\n или \n\r — считаем одним терминатором.
			if (term == '\r' && m.buf[next] == '\n') ||
				(term == '\n' && m.buf[next] == '\r') {
				next++
			}
		}
		m.buf = m.buf[next:]
		if line != "" {
			return line
		}
	}
}

// hasTerminator сообщает, есть ли в буфере \r или \n.
func hasTerminator(b []byte) bool {
	for _, c := range b {
		if c == '\r' || c == '\n' {
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
	start := time.Now()
	if _, err := m.port.Write([]byte(cmd + "\r")); err != nil {
		return "", fmt.Errorf("запись в порт: %w", err)
	}
	resp, err := m.waitFinal(cmd, timeout)
	m.debug("<< [%v]", time.Since(start).Round(time.Millisecond))
	return resp, err
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
	start := time.Now()
	if _, err := m.port.Write([]byte(hexPdu + "\x1A")); err != nil {
		return "", fmt.Errorf("запись PDU: %w", err)
	}
	resp, err := m.waitFinal("", finalWait)
	m.debug("<< [%v]", time.Since(start).Round(time.Millisecond))
	return resp, err
}

// drain читает и логирует все доступные строки, сбрасывает буфер.
// Вызывается перед отправкой команды, чтобы не получить остатки
// прошлых ответов.
//
// Если в буфере осталась неполная строка (нет \r и \n) —
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
