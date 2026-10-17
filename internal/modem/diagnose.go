package modem

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// DiagnoseResult — результат диагностики модема.
type DiagnoseResult struct {
	Info         string
	SIMStatus    string
	SignalRSSI   int // 0..31 или 99
	SignalDBM    int
	SignalGrade  string
	VoltageMV    int
	VoltageVolts float64
	VoltageLow   bool
	NetworkStat  int
	NetworkText  string
	Operator     string
}

// tryCommand выполняет команду, игнорируя ошибки.
func (m *Modem) tryCommand(cmd string, timeout time.Duration) string {
	r, err := m.Command(cmd, timeout)
	if err != nil {
		return ""
	}
	return r
}

// Diagnose собирает диагностическую информацию и печатает её через log.
// Возвращает структуру результата.
func (m *Modem) Diagnose(pin string) DiagnoseResult {
	res := DiagnoseResult{}

	res.Info = strings.TrimSpace(m.tryCommand("ATI", 3*time.Second))

	if err := m.UnlockSIM(pin); err != nil {
		res.SIMStatus = "ошибка: " + err.Error()
		m.log("SIM: " + err.Error())
	} else {
		res.SIMStatus = "READY"
	}

	if r := m.tryCommand("AT+CSQ", 3*time.Second); r != "" {
		if match := regexp.MustCompile(`\+CSQ:\s*(\d+)`).FindStringSubmatch(r); len(match) >= 2 {
			rssi, _ := strconv.Atoi(match[1])
			res.SignalRSSI = rssi
			if rssi == 99 {
				res.SignalGrade = "нет данных"
			} else {
				res.SignalDBM = -113 + 2*rssi
				switch {
				case rssi >= 20:
					res.SignalGrade = "отличный"
				case rssi >= 15:
					res.SignalGrade = "хороший"
				case rssi >= 10:
					res.SignalGrade = "удовлетворительный"
				default:
					res.SignalGrade = "слабый"
				}
			}
		}
	}

	if r := m.tryCommand("AT+CBC", 3*time.Second); r != "" {
		if match := regexp.MustCompile(`\+CBC:\s*\d+\s*,\s*\d+\s*,\s*(\d+)`).FindStringSubmatch(r); len(match) >= 2 {
			mv, _ := strconv.Atoi(match[1])
			res.VoltageMV = mv
			res.VoltageVolts = float64(mv) / 1000.0
			res.VoltageLow = res.VoltageVolts < 3.6
		}
	}

	if r := m.tryCommand("AT+CREG?", 3*time.Second); r != "" {
		if match := regexp.MustCompile(`\+CREG:\s*\d+\s*,\s*(\d+)`).FindStringSubmatch(r); len(match) >= 2 {
			stat, _ := strconv.Atoi(match[1])
			res.NetworkStat = stat
			res.NetworkText = describeCreg(stat)
		}
	}

	if r := m.tryCommand("AT+COPS?", 3*time.Second); r != "" {
		if match := regexp.MustCompile(`"(.+?)"`).FindStringSubmatch(r); len(match) >= 2 {
			res.Operator = match[1]
		}
	}

	m.printDiagnose(res)
	return res
}

// printDiagnose выводит результат диагностики через m.log.
func (m *Modem) printDiagnose(r DiagnoseResult) {
	if r.Info != "" {
		m.log("Модем: " + r.Info)
	}
	if r.SIMStatus != "" {
		m.log("SIM: " + r.SIMStatus)
	}
	if r.SignalRSSI != 0 {
		if r.SignalRSSI == 99 {
			m.log("Сигнал: нет данных (нет сети или не подключена антенна)")
		} else {
			m.log(fmt.Sprintf("Сигнал: %d/31 ≈ %d dBm, %s",
				r.SignalRSSI, r.SignalDBM, r.SignalGrade))
		}
	}
	if r.VoltageMV > 0 {
		warn := ""
		if r.VoltageLow {
			warn = " — низкое, модем может перезагружаться при передаче!"
		}
		m.log(fmt.Sprintf("Питание: %.2f В%s", r.VoltageVolts, warn))
	}
	if r.NetworkText != "" {
		m.log("Сеть: " + r.NetworkText)
	}
	if r.Operator != "" {
		m.log("Оператор: " + r.Operator)
	}
}

// describeCreg расшифровывает код регистрации +CREG.
func describeCreg(stat int) string {
	switch stat {
	case 0:
		return "не зарегистрирован, поиск сети не ведётся"
	case 1:
		return "зарегистрирован в домашней сети"
	case 2:
		return "идёт поиск сети"
	case 3:
		return "регистрация отклонена оператором"
	case 4:
		return "неизвестно"
	case 5:
		return "зарегистрирован (роуминг)"
	default:
		return fmt.Sprintf("неизвестно (%d)", stat)
	}
}

// WaitForNetwork ждёт регистрации в сети (status 1 или 5) или таймаута.
func (m *Modem) WaitForNetwork(timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	last := -2
	for time.Now().Before(deadline) {
		r, err := m.Command("AT+CREG?", 3*time.Second)
		if err != nil {
			return err
		}
		stat := -1
		if match := regexp.MustCompile(`\+CREG:\s*\d+\s*,\s*(\d+)`).FindStringSubmatch(r); len(match) >= 2 {
			stat, _ = strconv.Atoi(match[1])
		}
		if stat != last {
			m.log("Сеть: " + describeCreg(stat))
			last = stat
		}
		if stat == 1 || stat == 5 {
			return nil
		}
		if stat == 3 {
			return fmt.Errorf("оператор отклонил регистрацию; проверьте SIM в телефоне")
		}
		time.Sleep(2 * time.Second)
	}
	return fmt.Errorf("модем не зарегистрировался в сети за %s", timeout)
}

// SetPduMode включает PDU-режим (AT+CMGF=0).
func (m *Modem) SetPduMode() error {
	_, err := m.Command("AT+CMGF=0", 3*time.Second)
	return err
}

// Prepare выполняет базовую инициализацию и проверяет статус SIM.
// pin может быть пустым (если PIN не требуется). Вызывается в начале
// send, selftest, recv, interactive.
func (m *Modem) Prepare(pin string) error {
	if err := m.Sync(); err != nil {
		return err
	}
	if _, err := m.Command("ATE0", 2*time.Second); err != nil {
		return err
	}
	_, _ = m.Command("AT+CMEE=2", 2*time.Second)
	if _, err := m.Command("AT+CFUN=1", 5*time.Second); err != nil {
		return fmt.Errorf("AT+CFUN=1: %w", err)
	}
	// AT+COPS=0,0 — авто-выбор оператора. По SIM800L-NOTES.md
	// раздел 10 эта команда «тяжела для RF» и может занимать до
	// 30 секунд. Таймаут 10s давал ложные таймауты на исправном
	// модеме при слабом сигнале.
	_, _ = m.Command("AT+COPS=0,0", 30*time.Second)

	// AT+CNMI=2,1,0,1,0 — критические URC:
	//   mt=1 → +CMTI на входящие SMS
	//   ds=1 → +CDS на delivery reports
	// Без этого модем не присылает +CDS, и --delivery не работает.
	_, _ = m.Command("AT+CNMI=2,1,0,1,0", 3*time.Second)

	// AT+CMMS=2 — fast SMS sending (best-effort, не критично).
	_, _ = m.Command("AT+CMMS=2", 3*time.Second)

	// AT+CSMP=49,167,0,0 — установить параметры CSMP:
	//   fo=49 (0x31) — SRR=1, VPF=01 (запросить delivery report)
	//   vp=167       — validity period = 24 часа
	//   pid=0        — обычный
	//   dcs=0        — GSM-7 (переопределяется в PDU)
	// Некоторые прошивки SIM800L используют этот fo даже в PDU-режиме,
	// игнорируя SRR в PDU. Без этого --delivery не работает.
	_, _ = m.Command("AT+CSMP=49,167,0,0", 3*time.Second)

	_, _ = m.Command("AT+CREG=2", 3*time.Second)
	_, _ = m.Command("AT+CGREG=2", 3*time.Second)

	return m.EnsurePIN(pin)
}
