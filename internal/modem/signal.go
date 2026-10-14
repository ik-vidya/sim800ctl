// Package modem, файл signal.go — быстрая проверка наличия сигнала
// перед отправкой SMS. Помогает не тратить 90 секунд на ожидание
// сети, когда сигнала физически нет.
package modem

import (
	"fmt"
	"strings"
	"time"
)

// EnsureSignal проверяет, что у модема есть сигнал (CSQ rssi > 0).
// Возвращает понятную ошибку с рекомендацией, если сигнала нет.
//
// Используется в команде send перед WaitForNetwork, чтобы быстро
// сообщить пользователю о проблеме.
func (m *Modem) EnsureSignal() error {
	resp, err := m.Command("AT+CSQ", 3*time.Second)
	if err != nil {
		return fmt.Errorf("не удалось прочитать CSQ: %w", err)
	}
	rssi, ber := parseCSQBER(resp)
	if rssi <= 0 || rssi == 99 {
		return fmt.Errorf("нет сигнала (CSQ: %s). %s",
			strings.TrimSpace(resp),
			"Проверьте антенну и питание, затем запустите диагностику: "+
				"sim800ctl diagnose --test4 --test5")
	}
	m.log(fmt.Sprintf("Сигнал: %s", InterpretCSQ(rssi, ber)))
	return nil
}
