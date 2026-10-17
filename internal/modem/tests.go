// Package modem, файл tests.go — набор диагностических тестов для
// команды "diagnose". Тесты сгруппированы по назначению:
//
//	1–3 — ОСМОТР          (read-only, безопасно)
//	4–5 — ИЗМЕРЕНИЕ       (одно изменение состояния)
//	6–7 — ВОССТАНОВЛЕНИЕ  (сброс RF)
//	8   — ПРОВЕРКА        (пассивное наблюдение)
//
// Тесты запускаются через CLI:
//
//	sim800ctl -p /dev/ttyUSB0 diagnose --test1
//	sim800ctl -p /dev/ttyUSB0 diagnose --test1 --test2 --test3
package modem

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// TestResult — результат одного диагностического теста.
type TestResult struct {
	Name     string            // человекочитаемое название теста
	Passed   bool              // признак успешного завершения
	Findings []string          // наблюдения (что тест увидел)
	Hint     string            // рекомендация по устранению проблемы
	Params   map[string]string // структурированные значения для JSON-отчёта
}

// setParam добавляет параметр в Params, создавая карту при необходимости.
func (r *TestResult) setParam(key, value string) {
	if r.Params == nil {
		r.Params = make(map[string]string)
	}
	r.Params[key] = value
}

// KnownTests возвращает имена поддерживаемых тестов в порядке
// рекомендуемого применения: осмотр → измерение → восстановление → проверка.
func KnownTests() []string {
	return []string{
		"test1", "test2", "test3", // осмотр
		"test4", "test5", // измерение
		"test6", "test7", // восстановление
		"test8", // проверка
	}
}

// TestDescription возвращает краткое описание теста для справки.
func TestDescription(name string) string {
	switch name {
	case "test1":
		return "ОСМОТР: идентификация модема (AT, ATI, CGMI, CGMM, CGMR, CGSN)"
	case "test2":
		return "ОСМОТР: функциональный режим (CFUN?, CPAS, CSQ)"
	case "test3":
		return "ОСМОТР: состояние SIM (CPIN?, CCID, CIMI, SPIC)"
	case "test4":
		return "ИЗМЕРЕНИЕ: стабильность питания (CBC в покое и при поиске сети)"
	case "test5":
		return "ИЗМЕРЕНИЕ: сканирование сетей (AT+COPS=?, до 180 с)"
	case "test6":
		return "ВОССТАНОВЛЕНИЕ: мягкий сброс RF (AT+CFUN=1,1)"
	case "test7":
		return "ВОССТАНОВЛЕНИЕ: полный сброс модема (CFUN=0 → CFUN=1,1)"
	case "test8":
		return "ПРОВЕРКА: мониторинг регистрации 2 минуты (CSQ, CREG, CGREG, COPS)"
	}
	return ""
}

// RunTest запускает тест по имени и возвращает результат.
func (m *Modem) RunTest(name string) (TestResult, error) {
	switch name {
	case "test1":
		return m.testConnectivity(), nil
	case "test2":
		return m.testFunctionalMode(), nil
	case "test3":
		return m.testSIMState(), nil
	case "test4":
		return m.testPowerStability(), nil
	case "test5":
		return m.testNetworkScan(), nil
	case "test6":
		return m.testSoftRFReset(), nil
	case "test7":
		return m.testFullReset(), nil
	case "test8":
		return m.testRegistrationWatch(), nil
	}
	return TestResult{}, fmt.Errorf("неизвестный тест: %q", name)
}

// ==================== ГРУППА 1. ОСМОТР ====================

// testConnectivity — test1. Проверяет связь с модемом и читает
// идентификационные данные. Ничего не меняет.
func (m *Modem) testConnectivity() TestResult {
	r := TestResult{Name: "test1 — ОСМОТР: идентификация модема"}

	m.log("=== test1: идентификация модема ===")
	m.log("Цель: убедиться, что модем отвечает на AT, узнать модель, IMEI, прошивку.")
	m.log("Этот тест не трогает сеть и SIM. Безопасен для повторных запусков.")
	m.log("")

	if err := m.Sync(); err != nil {
		r.Findings = append(r.Findings, "модем не отвечает на AT: "+err.Error())
		r.Hint = "Проверьте кабель, порт, питание, соединение TX/RX и общий GND."
		return r
	}
	m.log("Связь с модемом установлена.")
	_, _ = m.Command("ATE0", 2*time.Second)

	for _, cmd := range []string{"ATI", "AT+CGMI", "AT+CGMM", "AT+CGMR", "AT+CGSN"} {
		resp, err := m.Command(cmd, 3*time.Second)
		if err != nil {
			m.log(fmt.Sprintf("%-10s → ошибка: %v", cmd, err))
			r.Findings = append(r.Findings, fmt.Sprintf("%s: ошибка %v", cmd, err))
			continue
		}
		resp = strings.TrimSpace(resp)
		m.log(fmt.Sprintf("%-10s → %s", cmd, resp))
		r.Findings = append(r.Findings, fmt.Sprintf("%s: %s", cmd, resp))

		switch cmd {
		case "AT+CGMM":
			r.setParam("model", resp)
		case "AT+CGMI":
			r.setParam("manufacturer", resp)
		case "AT+CGMR":
			r.setParam("revision", resp)
		case "AT+CGSN":
			r.setParam("imei", resp)
		}
	}

	r.Passed = true
	return r
}

// testFunctionalMode — test2. Только чтение: CFUN, CPAS, CSQ.
func (m *Modem) testFunctionalMode() TestResult {
	r := TestResult{Name: "test2 — ОСМОТР: функциональный режим"}

	m.log("=== test2: функциональный режим ===")
	m.log("Цель: прочитать CFUN (0 — минимум, 1 — полная работа), CPAS,")
	m.log("и CSQ. Тест только читает, ничего не меняет.")
	m.log("")

	if err := m.Sync(); err != nil {
		r.Findings = append(r.Findings, "модем не отвечает: "+err.Error())
		r.Hint = "Сначала запустите test1 для проверки связи."
		return r
	}
	_, _ = m.Command("ATE0", 2*time.Second)

	// CFUN?
	if resp, err := m.Command("AT+CFUN?", 3*time.Second); err == nil {
		resp = strings.TrimSpace(resp)
		code := parseSingleInt(resp, `\+CFUN:\s*(\d+)`)
		interp := InterpretCFUN(code)
		m.log(fmt.Sprintf("AT+CFUN? → %s   [%s]", resp, interp))
		r.Findings = append(r.Findings, fmt.Sprintf("CFUN: %s — %s", resp, interp))
		r.setParam("cfun", strconv.Itoa(code))
		if code == 0 {
			r.Hint = "CFUN=0 — радио выключено. Запустите test6 (мягкий сброс RF)."
		}
	} else {
		m.log("AT+CFUN? → ошибка: " + err.Error())
		r.Findings = append(r.Findings, "CFUN: ошибка "+err.Error())
	}

	// CPAS
	if resp, err := m.Command("AT+CPAS", 3*time.Second); err == nil {
		resp = strings.TrimSpace(resp)
		code := parseSingleInt(resp, `\+CPAS:\s*(\d+)`)
		interp := InterpretCPAS(code)
		m.log(fmt.Sprintf("AT+CPAS  → %s   [%s]", resp, interp))
		r.Findings = append(r.Findings, fmt.Sprintf("CPAS: %s — %s", resp, interp))
		r.setParam("cpas", strconv.Itoa(code))
	}

	// CSQ
	if resp, err := m.Command("AT+CSQ", 3*time.Second); err == nil {
		resp = strings.TrimSpace(resp)
		rssi, ber := parseCSQBER(resp)
		interp := InterpretCSQ(rssi, ber)
		m.log(fmt.Sprintf("AT+CSQ   → %s   [%s]", resp, interp))
		r.Findings = append(r.Findings, fmt.Sprintf("CSQ: %s — %s", resp, interp))
		r.setParam("csq_rssi", strconv.Itoa(rssi))
		r.setParam("csq_ber", strconv.Itoa(ber))
		switch {
		case rssi == 0:
			r.Hint = "CSQ=0 — RF-тракт не работает. Запустите test4 (питание) " +
				"и test5 (сканирование сетей)."
		case rssi == 99:
			r.Hint = "CSQ=99 — нет данных о сигнале. Возможно, модем ещё ищет сеть."
		case rssi < 10 && rssi >= 1:
			r.Hint = "Сигнал слабый. Проверьте антенну."
		}
	}

	r.Passed = true
	return r
}

// testSIMState — test3. Читает состояние SIM: CPIN, CCID, CIMI, SPIC.
func (m *Modem) testSIMState() TestResult {
	r := TestResult{Name: "test3 — ОСМОТР: состояние SIM-карты"}

	m.log("=== test3: состояние SIM-карты ===")
	m.log("Цель: проверить, что SIM читается корректно, узнать ICCID и IMSI,")
	m.log("а также число оставшихся попыток ввода PIN (если поддерживается).")
	m.log("")

	if err := m.Sync(); err != nil {
		r.Findings = append(r.Findings, "модем не отвечает: "+err.Error())
		return r
	}
	_, _ = m.Command("ATE0", 2*time.Second)

	if resp, err := m.Command("AT+CPIN?", 5*time.Second); err == nil {
		resp = strings.TrimSpace(resp)
		m.log("AT+CPIN?  → " + resp)
		r.Findings = append(r.Findings, "CPIN: "+resp)
		if i := strings.Index(resp, ":"); i >= 0 {
			r.setParam("cpin", strings.TrimSpace(resp[i+1:]))
		} else {
			r.setParam("cpin", resp)
		}
		if !strings.Contains(resp, "READY") {
			r.Hint = "SIM не в состоянии READY. Проверьте, вставлена ли карта, " +
				"не заблокирована ли она PIN или PUK."
		}
	} else {
		m.log("AT+CPIN?  → ошибка: " + err.Error())
		r.Findings = append(r.Findings, "CPIN: ошибка "+err.Error())
	}

	if resp, err := m.Command("AT+CCID", 5*time.Second); err == nil {
		resp = strings.TrimSpace(resp)
		m.log("AT+CCID   → " + resp)
		r.Findings = append(r.Findings, "CCID: "+resp)
		r.setParam("ccid", resp)
	} else {
		m.log("AT+CCID   → ошибка: " + err.Error())
	}

	if resp, err := m.Command("AT+CIMI", 5*time.Second); err == nil {
		resp = strings.TrimSpace(resp)
		m.log("AT+CIMI   → " + resp)
		r.Findings = append(r.Findings, "CIMI: "+resp)
		r.setParam("cimi", resp)
	} else {
		m.log("AT+CIMI   → ошибка: " + err.Error())
	}

	if resp, err := m.Command("AT+SPIC", 3*time.Second); err == nil {
		resp = strings.TrimSpace(resp)
		m.log("AT+SPIC   → " + resp)
		r.Findings = append(r.Findings, "SPIC: "+resp)
		r.setParam("spic", resp)
	} else {
		m.log("AT+SPIC   → не поддерживается (команда есть только на SIM800/900)")
	}

	r.Passed = true
	return r
}

// ==================== ГРУППА 2. ИЗМЕРЕНИЕ ====================

// testPowerStability — test4. Измеряет питание в покое и при поиске сети.
func (m *Modem) testPowerStability() TestResult {
	r := TestResult{Name: "test4 — ИЗМЕРЕНИЕ: стабильность питания"}

	m.log("=== test4: стабильность питания ===")
	m.log("Цель: замерить напряжение питания в покое и во время поиска сети.")
	m.log("Просадка ниже 3.6 В — типичная причина сбоев RF-секции SIM800L.")
	m.log("Длительность: около 60 секунд. Будет один кратковременный поиск сети.")
	m.log("")

	if err := m.Sync(); err != nil {
		r.Findings = append(r.Findings, "модем не отвечает: "+err.Error())
		r.Hint = "Сначала запустите test1 для проверки связи."
		return r
	}
	_, _ = m.Command("ATE0", 2*time.Second)

	// Замеры в покое.
	m.log("--- Замер 1: напряжение в покое (5 замеров за 10 секунд) ---")
	idleMin, idleMax := 99.0, 0.0
	for i := 0; i < 5; i++ {
		resp, err := m.Command("AT+CBC", 3*time.Second)
		if err != nil {
			m.log(fmt.Sprintf("  замер %d: ошибка %v", i+1, err))
		} else if c, ok := ParseCBC(resp); ok {
			m.log(fmt.Sprintf("  замер %d: %.2f В   [%s]", i+1, c.Volts, InterpretCBCVolts(c.Volts)))
			if c.Volts < idleMin {
				idleMin = c.Volts
			}
			if c.Volts > idleMax {
				idleMax = c.Volts
			}
		}
		time.Sleep(2 * time.Second)
	}
	if idleMin < 99 {
		r.Findings = append(r.Findings,
			fmt.Sprintf("в покое: min=%.2f, max=%.2f В", idleMin, idleMax))
		r.setParam("voltage_idle_min", fmt.Sprintf("%.2f", idleMin))
		r.setParam("voltage_idle_max", fmt.Sprintf("%.2f", idleMax))
	}

	// Форсируем поиск сети.
	m.log("")
	m.log("--- Запуск поиска сети (AT+CFUN=1,1) ---")
	if _, err := m.Command("AT+CFUN=1,1", 5*time.Second); err != nil {
		m.log("  ответа не последовало (ожидаемо при перезагрузке модема)")
	} else {
		m.log("  команда принята")
	}
	m.log("  пауза 10 секунд для перезапуска RF-секции...")
	time.Sleep(10 * time.Second)

	// Замеры во время поиска.
	m.log("")
	m.log("--- Замер 2: напряжение во время поиска сети (10 замеров за 30 секунд) ---")
	txMin, txMax := 99.0, 0.0
	for i := 0; i < 10; i++ {
		resp, err := m.Command("AT+CBC", 3*time.Second)
		if err != nil {
			m.log(fmt.Sprintf("  замер %d: ошибка %v", i+1, err))
		} else if c, ok := ParseCBC(resp); ok {
			m.log(fmt.Sprintf("  замер %d: %.2f В   [%s]", i+1, c.Volts, InterpretCBCVolts(c.Volts)))
			if c.Volts < txMin {
				txMin = c.Volts
			}
			if c.Volts > txMax {
				txMax = c.Volts
			}
		}
		time.Sleep(3 * time.Second)
	}
	if txMin < 99 {
		r.Findings = append(r.Findings,
			fmt.Sprintf("во время поиска: min=%.2f, max=%.2f В", txMin, txMax))
		r.setParam("voltage_tx_min", fmt.Sprintf("%.2f", txMin))
		r.setParam("voltage_tx_max", fmt.Sprintf("%.2f", txMax))
	}

	// Итог.
	m.log("")
	if idleMin < 99 && txMin < 99 {
		drop := idleMin - txMin
		m.log(fmt.Sprintf("Итог: просадка напряжения %.2f В", drop))
		r.setParam("voltage_drop", fmt.Sprintf("%.2f", drop))
		if txMin < 3.6 {
			r.Findings = append(r.Findings,
				"ПРОСАДКА НИЖЕ 3.6 В — вероятная причина сбоев RF")
			r.Hint = "Добавьте конденсатор 2200 мкФ параллельно питанию модема. " +
				"Используйте блок питания 5 В / 2 А. " +
				"Подключите модем напрямую к USB-порту, без хаба."
		} else if drop > 0.3 {
			r.Findings = append(r.Findings, "заметная просадка напряжения (>0.3 В)")
			r.Hint = "Питание нестабильно. Рекомендуется конденсатор 1000+ мкФ."
		} else {
			r.Passed = true
			r.Findings = append(r.Findings, "питание стабильно")
		}
	}
	return r
}

// testNetworkScan — test5. Сканирует сети и расшифровывает MCC-MNC.
func (m *Modem) testNetworkScan() TestResult {
	r := TestResult{Name: "test5 — ИЗМЕРЕНИЕ: сканирование сетей"}

	m.log("=== test5: сканирование сетей (AT+COPS=?) ===")
	m.log("Цель: узнать, каких операторов видит модем и кто из них текущий.")
	m.log("Длительность: до 180 секунд активного сканирования.")
	m.log("")

	if err := m.Sync(); err != nil {
		r.Findings = append(r.Findings, "модем не отвечает: "+err.Error())
		return r
	}
	_, _ = m.Command("ATE0", 2*time.Second)

	m.log("Сканирую доступные диапазоны...")
	resp, err := m.Command("AT+COPS=?", 180*time.Second)
	if err != nil {
		m.log("AT+COPS=? → ошибка: " + err.Error())
		r.Findings = append(r.Findings, "COPS=?: ошибка "+err.Error())

		// Fallback: короткие команды дают полезную информацию
		// даже когда сканирование не завершилось. Это не заменяет
		// AT+COPS=?, но помогает понять, видит ли модем сеть
		// вообще и в каком он состоянии.
		m.log("Fallback: короткие команды (без сканирования)")
		creg := strings.TrimSpace(m.tryCommand("AT+CREG?", 3*time.Second))
		cops := strings.TrimSpace(m.tryCommand("AT+COPS?", 3*time.Second))
		csq := strings.TrimSpace(m.tryCommand("AT+CSQ", 3*time.Second))

		if creg != "" {
			stat := parseSingleInt(creg, `\+CREG:\s*\d+\s*,\s*(\d+)`)
			m.log(fmt.Sprintf("  AT+CREG? → %s   [%s]", creg, InterpretCREG(stat)))
			r.Findings = append(r.Findings,
				fmt.Sprintf("CREG (fallback): %s — %s", creg, InterpretCREG(stat)))
			r.setParam("creg_fallback", strconv.Itoa(stat))
		}
		if cops != "" {
			m.log("  AT+COPS? → " + cops)
			r.Findings = append(r.Findings, "COPS? (fallback): "+cops)
		}
		if csq != "" {
			rssi, ber := parseCSQBER(csq)
			m.log(fmt.Sprintf("  AT+CSQ → %s   [%s]", csq, InterpretCSQ(rssi, ber)))
			r.Findings = append(r.Findings,
				fmt.Sprintf("CSQ (fallback): %s — %s", csq, InterpretCSQ(rssi, ber)))
			r.setParam("csq_fallback", strconv.Itoa(rssi))
		}

		// Подсказка по конкретному состоянию.
		cregStat := parseSingleInt(creg, `\+CREG:\s*\d+\s*,\s*(\d+)`)
		switch {
		case cregStat == 1 || cregStat == 5:
			r.Hint = "Модем зарегистрирован, но AT+COPS=? не завершился. " +
				"Это не обязательно сбой RF — сканирование может быть " +
				"очень медленным. Отправка SMS должна работать."
			r.Passed = true
		case cregStat == 2:
			r.Hint = "Модем в режиме поиска сети, сканирование не завершилось. " +
				"Проверьте питание (test4), антенну. " +
				"Попробуйте ручную регистрацию: " +
				`sim800ctl exec 'AT+COPS=1,2,"24702"'`
		case strings.Contains(csq, "0,0"):
			r.Hint = "CSQ=0 и AT+COPS=? не завершился — RF не работает. " +
				"Проверьте питание (test4) и антенну."
		default:
			r.Hint = "Сканирование не завершилось. Попробуйте увеличить " +
				"таймаут (--cops-timeout) или проверьте антенну."
		}
		return r
	}
	resp = strings.TrimSpace(resp)
	m.log("AT+COPS=? → " + resp)
	m.log("")

	ops := ParseCOPSList(resp)
	if len(ops) == 0 {
		r.Findings = append(r.Findings, "ПУСТОЙ СПИСОК ОПЕРАТОРОВ")
		r.Hint = "Модем не видит ни одного оператора. Проверьте антенну, " +
			"затем запустите test6 (мягкий сброс)."
		return r
	}

	m.log("Найдено операторов: " + strconv.Itoa(len(ops)))
	m.log("")
	m.log("  MCC-MNC   Оператор                          Статус")
	m.log("  --------  --------------------------------  -------------------------")
	currentCount := 0
	for _, op := range ops {
		name := op.Name
		if op.Known != "" && op.Known != op.Name {
			name = fmt.Sprintf("%s [%s]", op.Name, op.Known)
		}
		marker := " "
		if op.Current {
			marker = "*"
			currentCount++
			r.setParam("current_operator", op.MccMnc)
		}
		m.log(fmt.Sprintf("  %s %-7s  %-32s  %s",
			marker, op.MccMnc, name, op.StatusText()))
		r.Findings = append(r.Findings, op.Display())
	}
	r.setParam("operators_count", strconv.Itoa(len(ops)))

	if currentCount > 0 {
		r.Passed = true
		r.Findings = append(r.Findings,
			fmt.Sprintf("текущая сеть определена (%d)", currentCount))
	} else {
		r.Findings = append(r.Findings, "НЕТ ТЕКУЩЕЙ СЕТИ — регистрация не завершена")
		r.Hint = "Модем видит операторов, но ни к одному не подключён. " +
			"Запустите test8 (мониторинг) или test6 (мягкий сброс)."
	}
	return r
}

// ==================== ГРУППА 3. ВОССТАНОВЛЕНИЕ ====================

// testSoftRFReset — test6. Мягкий сброс RF через AT+CFUN=1,1.
func (m *Modem) testSoftRFReset() TestResult {
	r := TestResult{Name: "test6 — ВОССТАНОВЛЕНИЕ: мягкий сброс RF"}

	m.log("=== test6: мягкий сброс RF-тракта ===")
	m.log("Цель: перезапустить RF-секцию (CFUN=1,1) без предварительного")
	m.log("выключения радио. Менее радикально, чем test7.")
	m.log("Длительность: около 60 секунд.")
	m.log("")

	if err := m.Sync(); err != nil {
		r.Findings = append(r.Findings, "модем не отвечает: "+err.Error())
		return r
	}
	_, _ = m.Command("ATE0", 2*time.Second)

	m.log("Отправляю AT+CFUN=1,1")
	if _, err := m.Command("AT+CFUN=1,1", 5*time.Second); err != nil {
		m.log("  ответа нет (модем перезагружается) — это ожидаемо")
	} else {
		m.log("  команда принята")
	}
	m.log("  пауза 30 секунд...")
	time.Sleep(30 * time.Second)

	if err := m.Sync(); err != nil {
		m.log("  модем не отвечает после сброса: " + err.Error())
		r.Hint = "Модем не вернулся. Запустите test7 или отключите питание вручную."
		return r
	}

	m.log("  модем отвечает, ждём ещё 15 секунд...")
	time.Sleep(15 * time.Second)

	if resp, err := m.Command("AT+CSQ", 3*time.Second); err == nil {
		resp = strings.TrimSpace(resp)
		rssi, ber := parseCSQBER(resp)
		interp := InterpretCSQ(rssi, ber)
		m.log(fmt.Sprintf("  CSQ: %s   [%s]", resp, interp))
		r.Findings = append(r.Findings, fmt.Sprintf("CSQ: %s — %s", resp, interp))
		r.setParam("csq_rssi_after", strconv.Itoa(rssi))
		r.setParam("csq_ber_after", strconv.Itoa(ber))
		if rssi > 0 && rssi < 99 {
			r.Passed = true
			r.Findings = append(r.Findings, "СИГНАЛ ЕСТЬ — RF восстановлен")
		} else {
			r.Findings = append(r.Findings, "сигнал всё ещё 0")
			r.Hint = "Мягкий сброс не помог. Запустите test7 (полный сброс)."
		}
	}
	return r
}

// testFullReset — test7. Полный сброс: CFUN=0 → CFUN=1,1.
func (m *Modem) testFullReset() TestResult {
	r := TestResult{Name: "test7 — ВОССТАНОВЛЕНИЕ: полный сброс модема"}

	m.log("=== test7: полный сброс модема ===")
	m.log("Цель: восстановить RF-тракт через CFUN=0 → пауза → CFUN=1,1.")
	m.log("Это самый жёсткий программный сброс (эквивалент кратковременного")
	m.log("отключения питания). Длительность: около 90 секунд.")
	m.log("")

	if err := m.Sync(); err != nil {
		r.Findings = append(r.Findings, "модем не отвечает: "+err.Error())
		return r
	}
	_, _ = m.Command("ATE0", 2*time.Second)

	// Шаг 1: выключить радио.
	m.log("Шаг 1: AT+CFUN=0 (выключение радио)")
	if _, err := m.Command("AT+CFUN=0", 5*time.Second); err != nil {
		m.log("  ошибка: " + err.Error())
	} else {
		m.log("  OK")
	}
	m.log("  пауза 5 секунд...")
	time.Sleep(5 * time.Second)

	// Шаг 2: полный перезапуск.
	m.log("Шаг 2: AT+CFUN=1,1 (полный перезапуск стека)")
	if _, err := m.Command("AT+CFUN=1,1", 5*time.Second); err != nil {
		m.log("  ответа нет (модем перезагружается) — это ожидаемо")
	} else {
		m.log("  команда принята")
	}
	m.log("  пауза 30 секунд для перезапуска модема...")
	time.Sleep(30 * time.Second)

	// Шаг 3: проверка.
	m.log("")
	m.log("Шаг 3: проверка состояния (пауза ещё 15 секунд)")
	time.Sleep(15 * time.Second)

	if err := m.Sync(); err != nil {
		m.log("  модем не отвечает после сброса: " + err.Error())
		r.Hint = "Модем не вернулся после сброса. Отключите и подключите питание вручную."
		return r
	}
	m.log("  модем отвечает")

	if resp, err := m.Command("AT+CFUN?", 3*time.Second); err == nil {
		resp = strings.TrimSpace(resp)
		code := parseSingleInt(resp, `\+CFUN:\s*(\d+)`)
		m.log(fmt.Sprintf("  AT+CFUN? → %s   [%s]", resp, InterpretCFUN(code)))
		r.Findings = append(r.Findings, fmt.Sprintf("CFUN: %s — %s", resp, InterpretCFUN(code)))
		r.setParam("cfun_after", strconv.Itoa(code))
	}

	if resp, err := m.Command("AT+CSQ", 3*time.Second); err == nil {
		resp = strings.TrimSpace(resp)
		rssi, ber := parseCSQBER(resp)
		interp := InterpretCSQ(rssi, ber)
		m.log(fmt.Sprintf("  AT+CSQ   → %s   [%s]", resp, interp))
		r.Findings = append(r.Findings, fmt.Sprintf("CSQ: %s — %s", resp, interp))
		r.setParam("csq_rssi_after", strconv.Itoa(rssi))
		if rssi > 0 && rssi < 99 {
			r.Passed = true
			r.Findings = append(r.Findings, "СИГНАЛ ЕСТЬ — RF-тракт восстановлен")
		} else {
			r.Findings = append(r.Findings, "СИГНАЛ ВСЁ ЕЩЁ 0")
			r.Hint = "Полный сброс не помог. Отключите питание модема на 5 минут, " +
				"проверьте антенну. Если и это не поможет — вероятно повреждение модуля."
		}
	}

	time.Sleep(10 * time.Second)
	if resp, err := m.Command("AT+CREG?", 3*time.Second); err == nil {
		resp = strings.TrimSpace(resp)
		stat := parseSingleInt(resp, `\+CREG:\s*\d+\s*,\s*(\d+)`)
		m.log(fmt.Sprintf("  AT+CREG? → %s   [%s]", resp, InterpretCREG(stat)))
		r.Findings = append(r.Findings, fmt.Sprintf("CREG: %s — %s", resp, InterpretCREG(stat)))
		r.setParam("creg_after", strconv.Itoa(stat))
	}
	return r
}

// ==================== ГРУППА 4. ПРОВЕРКА ====================

// testRegistrationWatch — test8. Пассивно наблюдает за регистрацией 2 минуты.
func (m *Modem) testRegistrationWatch() TestResult {
	r := TestResult{Name: "test8 — ПРОВЕРКА: мониторинг регистрации"}

	m.log("=== test8: мониторинг регистрации ===")
	m.log("Цель: посмотреть динамику CSQ, CREG, CGREG, COPS в течение 2 минут.")
	m.log("Позволяет отличить временный сбой от устойчивой проблемы,")
	m.log("а также проверить стабильность после fix'а.")
	m.log("Длительность: 120 секунд.")
	m.log("")

	if err := m.Sync(); err != nil {
		r.Findings = append(r.Findings, "модем не отвечает: "+err.Error())
		return r
	}
	_, _ = m.Command("ATE0", 2*time.Second)
	_, _ = m.Command("AT+CREG=2", 3*time.Second)
	_, _ = m.Command("AT+CGREG=2", 3*time.Second)

	deadline := time.Now().Add(120 * time.Second)
	lastCSQ, lastCREG, lastCGREG := -1, -1, -1
	csqZeroSeen := false
	iter := 0

	for time.Now().Before(deadline) {
		iter++
		m.log(fmt.Sprintf("--- итерация %d (%s) ---", iter, time.Now().Format("15:04:05")))

		if resp, err := m.Command("AT+CSQ", 3*time.Second); err == nil {
			resp = strings.TrimSpace(resp)
			rssi, ber := parseCSQBER(resp)
			m.log(fmt.Sprintf("  CSQ:   %s   [%s]", resp, InterpretCSQ(rssi, ber)))
			if rssi >= 0 {
				lastCSQ = rssi
				if rssi == 0 {
					csqZeroSeen = true
				}
			}
		}
		if resp, err := m.Command("AT+CREG?", 3*time.Second); err == nil {
			resp = strings.TrimSpace(resp)
			stat := parseSingleInt(resp, `\+CREG:\s*\d+\s*,\s*(\d+)`)
			m.log(fmt.Sprintf("  CREG:  %s   [%s]", resp, InterpretCREG(stat)))
			if stat >= 0 {
				lastCREG = stat
			}
		}
		if resp, err := m.Command("AT+CGREG?", 3*time.Second); err == nil {
			resp = strings.TrimSpace(resp)
			stat := parseSingleInt(resp, `\+CGREG:\s*\d+\s*,\s*(\d+)`)
			m.log(fmt.Sprintf("  CGREG: %s   [%s]", resp, InterpretCGREG(stat)))
			if stat >= 0 {
				lastCGREG = stat
			}
		}
		if resp, err := m.Command("AT+COPS?", 3*time.Second); err == nil {
			resp = strings.TrimSpace(resp)
			if q, ok := ParseCOPSQuery(resp); ok {
				m.log(fmt.Sprintf("  COPS:  %s   [%s]", resp, q.String()))
			} else {
				m.log("  COPS:  " + resp)
			}
		}

		if lastCREG == 1 || lastCREG == 5 {
			m.log("Модем зарегистрирован в сети — мониторинг завершён.")
			r.Passed = true
			r.Findings = append(r.Findings,
				fmt.Sprintf("зарегистрирован: %s", InterpretCREG(lastCREG)))
			r.setParam("csq_rssi_final", strconv.Itoa(lastCSQ))
			r.setParam("creg_final", strconv.Itoa(lastCREG))
			r.setParam("cgreq_final", strconv.Itoa(lastCGREG))
			return r
		}
		time.Sleep(5 * time.Second)
	}

	r.Findings = append(r.Findings,
		fmt.Sprintf("последние значения: CSQ=%d (%s), CREG=%d (%s), CGREG=%d (%s)",
			lastCSQ, InterpretCSQ(lastCSQ, 0),
			lastCREG, InterpretCREG(lastCREG),
			lastCGREG, InterpretCGREG(lastCGREG)))
	r.setParam("csq_rssi_final", strconv.Itoa(lastCSQ))
	r.setParam("creg_final", strconv.Itoa(lastCREG))
	r.setParam("cgreq_final", strconv.Itoa(lastCGREG))

	if csqZeroSeen && lastCSQ == 0 {
		r.Findings = append(r.Findings, "CSQ всё время 0 — RF-тракт не работает")
		r.Hint = "Модем не видит сеть. Запустите test6 (мягкий RF-сброс). " +
			"Если не поможет — test7 (полный сброс), " +
			"затем отключение питания на 5 минут."
	} else if lastCREG == 2 {
		r.Findings = append(r.Findings, "модем находится в режиме поиска сети")
		r.Hint = "Сигнал есть, но регистрация не завершается. " +
			"Проверьте баланс SIM, состояние антенны, или запустите test5."
	}
	return r
}

// ==================== вспомогательные парсеры ====================

var (
	reCSQValue = regexp.MustCompile(`\+CSQ:\s*(\d+)`)
	reCREGStat = regexp.MustCompile(`\+CREG:\s*\d+\s*,\s*(\d+)`)
)

// parseCSQ извлекает rssi из ответа AT+CSQ.
func parseCSQ(resp string) (int, bool) {
	match := reCSQValue.FindStringSubmatch(resp)
	if len(match) < 2 {
		return 0, false
	}
	v, err := strconv.Atoi(match[1])
	if err != nil {
		return 0, false
	}
	return v, true
}

// parseCREG извлекает статус регистрации из ответа AT+CREG?.
func parseCREG(resp string) (int, bool) {
	match := reCREGStat.FindStringSubmatch(resp)
	if len(match) < 2 {
		return 0, false
	}
	v, err := strconv.Atoi(match[1])
	if err != nil {
		return 0, false
	}
	return v, true
}

// parseSingleInt извлекает первое целое число по регулярному выражению.
func parseSingleInt(resp, re string) int {
	match := regexp.MustCompile(re).FindStringSubmatch(resp)
	if len(match) < 2 {
		return -1
	}
	n, _ := strconv.Atoi(match[1])
	return n
}

// parseCSQBER извлекает rssi и ber из ответа AT+CSQ.
func parseCSQBER(resp string) (rssi, ber int) {
	match := regexp.MustCompile(`\+CSQ:\s*(\d+)\s*,\s*(\d+)`).FindStringSubmatch(resp)
	if len(match) < 3 {
		return -1, -1
	}
	rssi, _ = strconv.Atoi(match[1])
	ber, _ = strconv.Atoi(match[2])
	return
}
