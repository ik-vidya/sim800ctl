// Package modem, файл exec.go — произвольная AT-команда.
package modem

import (
	"fmt"
	"time"
)

// Exec выполняет одну AT-команду и возвращает ответ модема.
// timeout — таймаут ожидания. Если 0, используется 5 секунд.
//
// Используется командой CLI "exec" для отладки и ручных запросов.
func (m *Modem) Exec(cmd string, timeout time.Duration) (string, error) {
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	resp, err := m.Command(cmd, timeout)
	if err != nil {
		return resp, fmt.Errorf("команда %q: %w", cmd, err)
	}
	return resp, nil
}
