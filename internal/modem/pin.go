package modem

import (
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// pinQueryRetries — количество попыток проверки статуса SIM.
const (
	pinQueryRetries = 10
	pinQueryDelay   = 1 * time.Second
	pinReadyRetries = 15
	pinReadyDelay   = 1 * time.Second
)

// ErrPINLocked возвращается, когда SIM заблокирована и нужен PUK.
var ErrPINLocked = errors.New("SIM-карта заблокирована и требует PUK")

// ErrPINRejected возвращается, когда модем отклонил PIN.
var ErrPINRejected = errors.New("модем отклонил PIN")

// QuerySIM возвращает статус SIM с повторами.
func (m *Modem) QuerySIM() (string, error) {
	for i := 0; i < pinQueryRetries; i++ {
		r, err := m.Command("AT+CPIN?", 5*time.Second)
		if err != nil {
			if strings.Contains(strings.ToLower(err.Error()), "busy") {
				time.Sleep(pinQueryDelay)
				continue
			}
			return "", fmt.Errorf("ошибка запроса статуса SIM: %w", err)
		}
		if strings.Contains(r, "NOT READY") {
			time.Sleep(pinQueryDelay)
			continue
		}
		return r, nil
	}
	return "", fmt.Errorf("SIM-карта не готова после %d попыток", pinQueryRetries)
}

// pinAttemptsLeft возвращает число оставшихся попыток PIN через AT+SPIC
// (команда поддерживается SIM800/SIM900). Возвращает -1, если команда
// недоступна или не распознана.
func (m *Modem) pinAttemptsLeft() int {
	r, err := m.Command("AT+SPIC", 2*time.Second)
	if err != nil {
		return -1
	}
	match := regexp.MustCompile(`\+SPIC:\s*(\d+)`).FindStringSubmatch(r)
	if len(match) < 2 {
		return -1
	}
	n, _ := strconv.Atoi(match[1])
	return n
}

// UnlockSIM проверяет статус SIM и при необходимости вводит PIN.
// Защищена от «последней попытки»: если попытка осталась одна, отказ,
// чтобы пользователь не заблокировал карту по неосторожности.
func (m *Modem) UnlockSIM(pin string) error {
	r, err := m.QuerySIM()
	if err != nil {
		return err
	}
	if strings.Contains(r, "READY") {
		m.debug("SIM готова")
		return nil
	}
	if strings.Contains(r, "PUK") {
		return ErrPINLocked
	}
	if !strings.Contains(r, "SIM PIN") {
		return fmt.Errorf("неожиданный статус SIM: %q", r)
	}
	if pin == "" {
		return errors.New("SIM-карта запрашивает PIN; укажите --pin")
	}
	if len(pin) < 4 || len(pin) > 8 {
		return errors.New("PIN должен содержать 4–8 цифр")
	}
	for _, c := range pin {
		if c < '0' || c > '9' {
			return errors.New("PIN должен содержать только цифры")
		}
	}

	if left := m.pinAttemptsLeft(); left >= 0 {
		m.log(fmt.Sprintf("осталось попыток ввода PIN: %d", left))
		if left == 1 {
			return errors.New(
				"осталась последняя попытка ввода PIN. " +
					"Введите PIN в телефоне, чтобы не заблокировать SIM")
		}
	}

	// Маскируем PIN в логе.
	m.mu.Lock()
	m.drain()
	m.debug(">> AT+CPIN=\"****\"")
	_, err = m.port.Write([]byte(fmt.Sprintf("AT+CPIN=%q\r", pin)))
	if err != nil {
		m.mu.Unlock()
		return fmt.Errorf("запись PIN: %w", err)
	}
	_, err = m.waitFinal("", 10*time.Second)
	m.mu.Unlock()
	if err != nil {
		return fmt.Errorf("%w: %v", ErrPINRejected, err)
	}
	m.log("PIN принят")

	for i := 0; i < pinReadyRetries; i++ {
		time.Sleep(pinReadyDelay)
		if r, _ := m.QuerySIM(); strings.Contains(r, "READY") {
			m.log("SIM готова")
			return nil
		}
	}
	return errors.New("SIM не перешла в READY после ввода PIN")
}

// EnsurePIN проверяет, что SIM готова (READY) или что указан PIN для
// разблокировки. Возвращает понятную ошибку заранее, не дожидаясь
// падения в середине длинной операции.
//
// Вызывается из Prepare(), чтобы пользователь сразу видел, что забыл
// указать --pin.
func (m *Modem) EnsurePIN(pin string) error {
	resp, err := m.Command("AT+CPIN?", 5*time.Second)
	if err != nil {
		return fmt.Errorf("не удалось прочитать статус SIM: %w", err)
	}
	resp = strings.TrimSpace(resp)
	switch {
	case strings.Contains(resp, "READY"):
		return nil
	case strings.Contains(resp, "SIM PIN"):
		if pin == "" {
			return fmt.Errorf("SIM требует PIN; укажите --pin <цифры>")
		}
		return m.UnlockSIM(pin)
	case strings.Contains(resp, "SIM PUK"):
		return fmt.Errorf("SIM заблокирована и требует PUK; разблокируйте её в телефоне")
	case strings.Contains(resp, "NOT INSERTED"):
		return fmt.Errorf("SIM не вставлена")
	case strings.Contains(resp, "NOT READY"):
		return fmt.Errorf("SIM не готова (NOT READY)")
	}
	return fmt.Errorf("неожиданный статус SIM: %q", resp)
}
