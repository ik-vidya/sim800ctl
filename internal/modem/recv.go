// Package modem, файл recv.go — чтение входящих SMS.
//
// Поддерживает два режима:
//   - PDU (AT+CMGF=0) — рекомендуется, читает любые сообщения корректно;
//   - text (AT+CMGF=1) + UCS2 — проще, но ломается на GSM-7 от SIM800L.
package modem

import (
	"encoding/hex"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/ik-vidya/sim800ctl/pkg/pdu"
)

// SMSMessage — одно принятое SMS.
type SMSMessage struct {
	Index    int
	Stat     string
	From     string
	SCTS     string
	Text     string
	RawLine  string
	RawPDU   string // исходный hex-PDU (если читали в PDU-режиме)
	Encoding string // "GSM-7", "UCS-2"
}

// ReadAllPDU читает все SMS в PDU-режиме. Рекомендуется — корректно
// разбирает и GSM-7, и UCS-2 независимо от прошивки модема.
func (m *Modem) ReadAllPDU() ([]SMSMessage, error) {
	if _, err := m.Command("AT+CMGF=0", 3*time.Second); err != nil {
		return nil, fmt.Errorf("не удалось включить PDU mode: %w", err)
	}
	if err := m.alignReadStorage(); err != nil {
		m.log("предупреждение: не удалось выровнять storage: " + err.Error())
	}
	resp, err := m.Command("AT+CMGL=4", 10*time.Second)
	if err != nil {
		if strings.Contains(err.Error(), "ERROR") {
			return nil, nil
		}
		return nil, fmt.Errorf("AT+CMGL=4: %w", err)
	}
	return parseCMGLPDU(resp), nil
}

// ReadUnreadPDU читает только непрочитанные (статус 0) в PDU-режиме.
func (m *Modem) ReadUnreadPDU() ([]SMSMessage, error) {
	if _, err := m.Command("AT+CMGF=0", 3*time.Second); err != nil {
		return nil, fmt.Errorf("не удалось включить PDU mode: %w", err)
	}
	if err := m.alignReadStorage(); err != nil {
		m.log("предупреждение: не удалось выровнять storage: " + err.Error())
	}
	resp, err := m.Command("AT+CMGL=0", 10*time.Second)
	if err != nil {
		if strings.Contains(err.Error(), "ERROR") {
			return nil, nil
		}
		return nil, fmt.Errorf("AT+CMGL=0: %w", err)
	}
	return parseCMGLPDU(resp), nil
}

// ReadAllSMS — совместимость: читает в text-режиме (устаревший путь).
// Используйте ReadAllPDU для корректного декодирования.
func (m *Modem) ReadAllSMS() ([]SMSMessage, error) {
	if err := m.enterTextMode(); err != nil {
		return nil, err
	}
	resp, err := m.Command(`AT+CMGL="ALL"`, 10*time.Second)
	if err != nil {
		if strings.Contains(err.Error(), "ERROR") {
			return nil, nil
		}
		return nil, fmt.Errorf("AT+CMGL: %w", err)
	}
	return parseCMGL(resp), nil
}

// ReadUnreadSMS — совместимость: text-режим.
func (m *Modem) ReadUnreadSMS() ([]SMSMessage, error) {
	if err := m.enterTextMode(); err != nil {
		return nil, err
	}
	resp, err := m.Command(`AT+CMGL="REC UNREAD"`, 10*time.Second)
	if err != nil {
		if strings.Contains(err.Error(), "ERROR") {
			return nil, nil
		}
		return nil, fmt.Errorf("AT+CMGL: %w", err)
	}
	return parseCMGL(resp), nil
}

func (m *Modem) enterTextMode() error {
	if _, err := m.Command("AT+CMGF=1", 3*time.Second); err != nil {
		return fmt.Errorf("не удалось включить text mode: %w", err)
	}
	if _, err := m.Command(`AT+CSCS="UCS2"`, 3*time.Second); err != nil {
		if _, err2 := m.Command(`AT+CSCS="GSM"`, 3*time.Second); err2 != nil {
			return fmt.Errorf("не удалось установить кодировку: %w", err)
		}
	}
	if err := m.alignReadStorage(); err != nil {
		m.log("предупреждение: не удалось выровнять storage: " + err.Error())
	}
	return nil
}

type cpmsState struct {
	read, write, receive string
}

var reCPMS = regexp.MustCompile(
	`\+CPMS:\s*"([^"]*)",\d+,\d+,"([^"]*)",\d+,\d+,"([^"]*)",\d+,\d+`)

func parseCPMS(resp string) cpmsState {
	m := reCPMS.FindStringSubmatch(resp)
	if len(m) < 4 {
		return cpmsState{}
	}
	return cpmsState{read: m[1], write: m[2], receive: m[3]}
}

func (m *Modem) alignReadStorage() error {
	resp, err := m.Command("AT+CPMS?", 3*time.Second)
	if err != nil {
		return err
	}
	resp = strings.TrimSpace(resp)
	m.log("AT+CPMS? → " + resp)

	st := parseCPMS(resp)
	if st.receive == "" || st.read == st.receive {
		return nil
	}
	m.log(fmt.Sprintf("storage: read=%s, receive=%s — выравниваю read → %s",
		st.read, st.receive, st.receive))
	_, err = m.Command(fmt.Sprintf(`AT+CPMS="%s"`, st.receive), 3*time.Second)
	return err
}

// DeleteSMS удаляет сообщение по индексу.
func (m *Modem) DeleteSMS(index int) error {
	if err := m.alignReadStorage(); err != nil {
		m.log("предупреждение: не удалось выровнять storage: " + err.Error())
	}
	_, err := m.Command(fmt.Sprintf("AT+CMGD=%d", index), 5*time.Second)
	return err
}

// DeleteAllSMS удаляет все сообщения из памяти модема.
func (m *Modem) DeleteAllSMS() error {
	if err := m.alignReadStorage(); err != nil {
		m.log("предупреждение: не удалось выровнять storage: " + err.Error())
	}
	_, err := m.Command("AT+CMGD=1,4", 10*time.Second)
	return err
}

// parseCMGLPDU разбирает ответ AT+CMGL=4 в PDU-режиме.
//
// Формат:
//
//	+CMGL: 1,0,,26
//	079172...
//	+CMGL: 2,1,,26
//	079172...
//	OK
func parseCMGLPDU(resp string) []SMSMessage {
	lines := strings.Split(resp, "\n")
	var messages []SMSMessage
	var current *SMSMessage

	flush := func() {
		if current != nil && current.RawPDU != "" {
			if d, err := pdu.Decode(current.RawPDU); err == nil {
				current.From = d.Peer
				current.Text = d.Text
				current.Encoding = d.Encoding
				if !d.Timestamp.IsZero() {
					current.SCTS = d.Timestamp.Format("2006-01-02 15:04:05 -0700")
				}
			}
			messages = append(messages, *current)
		}
		current = nil
	}

	for _, raw := range lines {
		line := strings.TrimSpace(raw)
		if line == "" || line == "OK" || line == "ERROR" {
			continue
		}
		if strings.HasPrefix(line, "+CMGL:") {
			flush()
			current = &SMSMessage{RawLine: line}
			parseCMGLPDUHeader(line, current)
			continue
		}
		if current != nil && current.RawPDU == "" {
			if isHexLine(line) {
				current.RawPDU = line
			}
		}
	}
	flush()
	return messages
}

// parseCMGLPDUHeader разбирает строку "+CMGL: <idx>,<stat>,[<alpha>],<len>".
func parseCMGLPDUHeader(line string, msg *SMSMessage) {
	body := strings.TrimSpace(strings.TrimPrefix(line, "+CMGL:"))
	parts := strings.Split(body, ",")
	if len(parts) < 1 {
		return
	}
	msg.Index, _ = strconv.Atoi(strings.TrimSpace(parts[0]))
	if len(parts) >= 2 {
		statCode, _ := strconv.Atoi(strings.TrimSpace(parts[1]))
		msg.Stat = pduStatText(statCode)
	}
}

func pduStatText(code int) string {
	switch code {
	case 0:
		return "REC UNREAD"
	case 1:
		return "REC READ"
	case 2:
		return "STO UNSENT"
	case 3:
		return "STO SENT"
	case 4:
		return "ALL"
	}
	return fmt.Sprintf("STAT %d", code)
}

func isHexLine(s string) bool {
	if len(s) < 4 || len(s)%2 != 0 {
		return false
	}
	_, err := hex.DecodeString(s)
	return err == nil
}

// parseCMGL разбирает ответ AT+CMGL в текстовом режиме.
func parseCMGL(resp string) []SMSMessage {
	lines := strings.Split(resp, "\n")
	var messages []SMSMessage
	var current *SMSMessage

	flush := func() {
		if current != nil {
			messages = append(messages, *current)
			current = nil
		}
	}

	for _, raw := range lines {
		line := strings.TrimSpace(raw)
		if line == "" || line == "OK" || line == "ERROR" {
			continue
		}
		if strings.HasPrefix(line, "+CMGL:") {
			flush()
			current = &SMSMessage{RawLine: line, Encoding: "UCS-2"}
			parseCMGLHeader(line, current)
			continue
		}
		if current != nil && current.Text == "" {
			if text, err := DecodeUCS2Hex(line); err == nil {
				current.Text = text
			} else {
				current.Text = line
			}
		}
	}
	flush()
	return messages
}

// parseCMGLHeader разбирает "+CMGL: <idx>,"<stat>","<oa>","<alpha>","<scts>"".
// SCTS содержит запятую внутри кавычек, поэтому разбираем по кавычкам.
func parseCMGLHeader(line string, msg *SMSMessage) {
	body := strings.TrimSpace(strings.TrimPrefix(line, "+CMGL:"))
	i := 0
	for i < len(body) && (body[i] == ' ' || body[i] == '\t') {
		i++
	}
	j := i
	for j < len(body) && body[j] >= '0' && body[j] <= '9' {
		j++
	}
	msg.Index, _ = strconv.Atoi(body[i:j])

	var fields []string
	for j < len(body) {
		if body[j] == '"' {
			k := j + 1
			for k < len(body) && body[k] != '"' {
				k++
			}
			fields = append(fields, body[j+1:k])
			j = k + 1
		} else {
			j++
		}
	}
	if len(fields) >= 1 {
		msg.Stat = fields[0]
	}
	if len(fields) >= 2 {
		oaRaw := fields[1]
		if oa, err := DecodeUCS2Hex(oaRaw); err == nil {
			msg.From = oa
		} else {
			msg.From = oaRaw
		}
	}
	switch len(fields) {
	case 3:
		msg.SCTS = fields[2]
	case 4:
		msg.SCTS = fields[3]
	}
}

func trimQuotes(s string) string {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, `"`)
	s = strings.TrimSuffix(s, `"`)
	return s
}

// DecodeUCS2Hex декодирует hex-строку UCS-2 BE.
func DecodeUCS2Hex(s string) (string, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return "", fmt.Errorf("пустая строка")
	}
	b, err := hex.DecodeString(s)
	if err != nil {
		return "", fmt.Errorf("не hex: %w", err)
	}
	if len(b)%2 != 0 {
		return "", fmt.Errorf("нечётная длина UCS2")
	}
	var sb strings.Builder
	for i := 0; i < len(b); i += 2 {
		unit := uint16(b[i])<<8 | uint16(b[i+1])
		if unit >= 0xD800 && unit <= 0xDBFF && i+3 < len(b) {
			next := uint16(b[i+2])<<8 | uint16(b[i+3])
			if next >= 0xDC00 && next <= 0xDFFF {
				r := rune(unit-0xD800)<<10 | rune(next-0xDC00) + 0x10000
				sb.WriteRune(r)
				i += 2
				continue
			}
		}
		sb.WriteRune(rune(unit))
	}
	return sb.String(), nil
}
