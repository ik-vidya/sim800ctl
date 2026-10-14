// Package modem, файл send_retry.go — повторные попытки отправки SMS
// при транзиентных ошибках сети ("Temporary failure", "No network service"
// и т.п.).
package modem

import (
	"fmt"
	"strings"
	"time"
)

// SendPduWithRetry отправляет PDU с повторными попытками при транзиентных
// ошибках. Возвращает результат успешной попытки или последнюю ошибку,
// если все попытки исчерпаны или если ошибка постоянная.
//
// Параметры:
//   - hexPdu     — PDU в hex-виде (без октета SMSC, как для AT+CMGS)
//   - tpduLength — значение для AT+CMGS=<len>
//   - maxAttempts — общее число попыток (включая первую). Минимум 1.
//   - backoff    — базовая задержка. Пауза перед попыткой N = N*backoff.
//     Например, при backoff=5s: пауза перед 2-й попыткой 5s,
//     перед 3-й — 10s.
func (m *Modem) SendPduWithRetry(hexPdu string, tpduLength, maxAttempts int, backoff time.Duration) (string, error) {
	if maxAttempts < 1 {
		maxAttempts = 1
	}

	var lastErr error
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		resp, err := m.SendPdu(hexPdu, tpduLength)
		if err == nil {
			if attempt > 1 {
				m.log(fmt.Sprintf("Попытка %d/%d успешна.", attempt, maxAttempts))
			}
			return resp, nil
		}
		lastErr = err

		// Постоянная ошибка — не тратим время на повторы.
		if !isTransientSendError(err) {
			return "", err
		}

		if attempt == maxAttempts {
			break
		}

		wait := time.Duration(attempt) * backoff
		m.log(fmt.Sprintf(
			"Попытка %d/%d не удалась: %v. Повтор через %s...",
			attempt, maxAttempts, err, wait))
		time.Sleep(wait)
	}
	return "", fmt.Errorf("после %d попыток: %w", maxAttempts, lastErr)
}

// isTransientSendError — это транзиентная ошибка отправки, при которой
// стоит попробовать ещё раз?
func isTransientSendError(err error) bool {
	if err == nil {
		return false
	}
	s := strings.ToLower(err.Error())

	// Текстовые ответы SIM800.
	for _, marker := range []string{
		"temporary failure",
		"no network service",
		"network timeout",
		"network busy",
	} {
		if strings.Contains(s, marker) {
			return true
		}
	}

	// Числовые коды +CMS ERROR.
	for _, code := range []string{
		"+cms error: 38",  // network out of order
		"+cms error: 41",  // temporary failure
		"+cms error: 42",  // congestion
		"+cms error: 47",  // resources unavailable
		"+cms error: 331", // no network service
		"+cms error: 332", // network timeout
		"+cms error: 500", // unknown error (часто транзиентная)
	} {
		if strings.Contains(s, code) {
			return true
		}
	}
	return false
}
