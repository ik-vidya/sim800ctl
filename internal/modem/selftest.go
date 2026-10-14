// Package modem, файл selftest.go — самопроверка модема: отправка SMS
// самому себе и проверка получения через PDU-декодер.
//
// Сценарий:
//  1. Убедиться, что модем работает (Prepare, EnsureSignal).
//  2. Дождаться регистрации в сети.
//  3. Сформировать SMS с уникальным текстом (timestamp + случайный
//     идентификатор).
//  4. Отправить SMS через AT+CMGS с ForceUCS2 (обход бага SIM800L).
//  5. Периодически читать входящие через PDU-режим (ReadAllPDU).
//  6. Найти совпадение по тексту. Если найдено — успех.
//
// Это проверяет весь тракт: модем → сеть → SMSC → сеть → модем.
package modem

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"github.com/ik-vidya/sim800ctl/pkg/pdu"
)

// SelfTestResult — результат самопроверки. Теги JSON — для вывода
// через --json в CLI.
type SelfTestResult struct {
	TestText   string        `json:"test_text"`
	SentRef    string        `json:"sent_ref,omitempty"`
	SentAt     time.Time     `json:"sent_at"`
	Received   bool          `json:"received"`
	RecvText   string        `json:"recv_text,omitempty"`
	RecvFrom   string        `json:"recv_from,omitempty"`
	RecvAt     time.Time     `json:"recv_at,omitempty"`
	WaitTime   time.Duration `json:"-"`       // внутреннее поле
	WaitMillis int64         `json:"wait_ms"` // для JSON-вывода
	Notes      []string      `json:"notes,omitempty"`
}

// SelfTest отправляет SMS на указанный номер и ждёт его возврата.
//
// Параметры:
//   - to:      номер получателя (обычно собственный номер модема)
//   - text:    базовый текст (к нему добавится уникальный маркер)
//   - timeout: сколько ждать появления входящего
//
// Ошибка — только если что-то сломалось на этапе подготовки или отправки.
// Если SMS не вернулось за timeout — возвращает результат с Received=false
// без ошибки.
func (m *Modem) SelfTest(to, text string, timeout time.Duration) (SelfTestResult, error) {
	res := SelfTestResult{SentAt: time.Now()}

	token := randomToken(4)
	res.TestText = fmt.Sprintf("%s [%s]", strings.TrimSpace(text), token)
	res.Notes = append(res.Notes, "уникальный маркер: "+token)

	// PDU mode для отправки.
	if _, err := m.Command("AT+CMGF=0", 3*time.Second); err != nil {
		return res, fmt.Errorf("не удалось включить PDU mode: %w", err)
	}

	// ForceUCS2: даже ASCII-текст отправляем как UCS-2, чтобы SIM800L
	// не искажал его при чтении в text mode (баг прошивки).
	parts, err := pdu.BuildWith(to, res.TestText, pdu.BuildOptions{ForceUCS2: true})
	if err != nil {
		return res, fmt.Errorf("кодирование PDU: %w", err)
	}
	if len(parts) != 1 {
		return res, fmt.Errorf("текст слишком длинный: %d частей", len(parts))
	}

	m.log(fmt.Sprintf("Отправляю самотест на %s: %q", to, res.TestText))
	resp, err := m.SendPduWithRetry(parts[0].Hex, parts[0].TpduLength, 3, 5*time.Second)
	if err != nil {
		return res, fmt.Errorf("отправка самотеста: %w", err)
	}
	res.SentRef = extractCMGSRef(resp)
	m.log("SMS отправлено, reference: " + res.SentRef)

	// Дальнейшее чтение — в PDU-режиме (корректно и GSM-7, и UCS-2).
	m.log(fmt.Sprintf("Ожидаю эхо до %s...", timeout))
	deadline := time.Now().Add(timeout)
	pollInterval := 5 * time.Second

	for time.Now().Before(deadline) {
		time.Sleep(pollInterval)

		msgs, err := m.ReadAllPDU()
		if err != nil {
			res.Notes = append(res.Notes,
				fmt.Sprintf("ошибка чтения (не критично): %v", err))
			continue
		}
		for _, msg := range msgs {
			if strings.Contains(msg.Text, token) {
				res.Received = true
				res.RecvText = msg.Text
				res.RecvFrom = msg.From
				res.RecvAt = time.Now()
				res.WaitTime = res.RecvAt.Sub(res.SentAt)
				res.WaitMillis = res.WaitTime.Milliseconds()
				return res, nil
			}
		}
	}

	res.WaitTime = time.Since(res.SentAt)
	res.WaitMillis = res.WaitTime.Milliseconds()
	return res, nil
}

// extractCMGSRef извлекает reference из ответа "+CMGS: 123\nOK".
func extractCMGSRef(resp string) string {
	for _, line := range strings.Split(resp, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "+CMGS:") {
			return strings.TrimSpace(strings.TrimPrefix(line, "+CMGS:"))
		}
	}
	return ""
}

// randomToken генерирует случайный hex-токен длиной n байт.
func randomToken(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("%08x", time.Now().UnixNano()&0xFFFFFFFF)
	}
	return hex.EncodeToString(b)
}
