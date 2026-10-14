// Package modem, файл interpret.go — человекочитаемые расшифровки
// ответов модема. Используется в диагностических тестах, чтобы
// пользователь видел не «CREG: 2,5», а «зарегистрирован (роуминг)».
package modem

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// ==================== расшифровка одиночных значений ====================

// InterpretCSQ расшифровывает ответ AT+CSQ (rssi, ber).
func InterpretCSQ(rssi, ber int) string {
	if rssi == 99 {
		return "нет данных о сигнале (сеть не найдена или RF выключен)"
	}
	var quality string
	switch {
	case rssi >= 20:
		quality = "отличный"
	case rssi >= 15:
		quality = "хороший"
	case rssi >= 10:
		quality = "удовлетворительный"
	case rssi >= 5:
		quality = "слабый"
	case rssi >= 1:
		quality = "очень слабый"
	default:
		quality = "нет сигнала"
	}
	dbm := -113 + 2*rssi
	return fmt.Sprintf("rssi=%d/31, BER=%d, ≈%d dBm, %s", rssi, ber, dbm, quality)
}

// InterpretCREG расшифровывает статус регистрации (второе число +CREG).
func InterpretCREG(stat int) string {
	switch stat {
	case 0:
		return "не зарегистрирован, поиск не ведётся"
	case 1:
		return "зарегистрирован в домашней сети"
	case 2:
		return "идёт поиск сети"
	case 3:
		return "регистрация отклонена оператором"
	case 4:
		return "неизвестно (состояние модема не определено)"
	case 5:
		return "зарегистрирован (роуминг)"
	}
	return fmt.Sprintf("неизвестный статус %d", stat)
}

// InterpretCGREG расшифровывает статус GPRS-регистрации (второе число +CGREG).
// Коды статуса совпадают с CREG по 3GPP TS 27.007 §7.2.
func InterpretCGREG(stat int) string {
	switch stat {
	case 0:
		return "не зарегистрирован в GPRS"
	case 1:
		return "зарегистрирован в GPRS (домашняя сеть)"
	case 2:
		return "идёт поиск GPRS"
	case 3:
		return "GPRS-регистрация отклонена"
	case 4:
		return "GPRS: неизвестно"
	case 5:
		return "зарегистрирован в GPRS (роуминг)"
	}
	return fmt.Sprintf("GPRS: неизвестный статус %d", stat)
}

// InterpretCPAS расшифровывает ответ AT+CPAS.
func InterpretCPAS(code int) string {
	switch code {
	case 0:
		return "ready — готов к AT-командам"
	case 1:
		return "unavailable — недоступен (нет SIM или питания)"
	case 2:
		return "unknown — состояние не определено (обычно норма)"
	case 3:
		return "ringing — входящий звонок"
	case 4:
		return "call in progress — идёт звонок"
	case 5:
		return "asleep — спящий режим (модем экономит питание)"
	}
	return fmt.Sprintf("неизвестный код %d", code)
}

// InterpretCFUN расшифровывает ответ AT+CFUN?.
func InterpretCFUN(code int) string {
	switch code {
	case 0:
		return "minimum functionality — радио выключено"
	case 1:
		return "full functionality — полная работа"
	case 4:
		return "disable phone TX and RX — передача запрещена"
	}
	return fmt.Sprintf("неизвестный код %d", code)
}

// InterpretCBCVolts расшифровывает напряжение питания.
func InterpretCBCVolts(volts float64) string {
	switch {
	case volts >= 4.0:
		return "отличное"
	case volts >= 3.7:
		return "нормальное"
	case volts >= 3.6:
		return "на нижней границе (риск при пиковой нагрузке)"
	case volts >= 3.3:
		return "низкое (модем может перезагружаться при передаче)"
	default:
		return "критически низкое"
	}
}

// ==================== разбор AT+CBC полностью ====================

// CBCInfo — разобранный ответ AT+CBC.
//
// Формат: +CBC: <bcs>, <bcl>, <voltage>
//   - bcs     — статус зарядки (0 = не заряжается, 1 = заряжается, 2 = завершено)
//   - bcl     — уровень заряда в процентах (0..100)
//   - voltage — напряжение в милливольтах
type CBCInfo struct {
	Status  int
	Percent int
	MilliV  int
	Volts   float64
}

var reCBCFull = regexp.MustCompile(`\+CBC:\s*(\d+)\s*,\s*(\d+)\s*,\s*(\d+)`)

// ParseCBC разбирает ответ AT+CBC.
func ParseCBC(resp string) (CBCInfo, bool) {
	m := reCBCFull.FindStringSubmatch(resp)
	if len(m) < 4 {
		return CBCInfo{}, false
	}
	var c CBCInfo
	c.Status, _ = strconv.Atoi(m[1])
	c.Percent, _ = strconv.Atoi(m[2])
	c.MilliV, _ = strconv.Atoi(m[3])
	c.Volts = float64(c.MilliV) / 1000.0
	return c, true
}

// String возвращает человекочитаемое представление.
func (c CBCInfo) String() string {
	return fmt.Sprintf("заряд=%d%%, %.2f В — %s",
		c.Percent, c.Volts, InterpretCBCVolts(c.Volts))
}

// ==================== разбор AT+COPS? ====================

// COPSQuery — разобранный ответ AT+COPS? (текущий выбранный оператор).
//
// Формат: +COPS: <mode>[,<format>, "<oper>"[,<act>]]
type COPSQuery struct {
	Mode    int
	Format  int
	Oper    string
	Act     int
	HasOper bool
}

var reCOPSQuery = regexp.MustCompile(`\+COPS:\s*(\d+)(?:\s*,\s*(\d+)(?:\s*,\s*"([^"]*)")?(?:\s*,\s*(\d+))?)?`)

// ParseCOPSQuery разбирает ответ AT+COPS?.
func ParseCOPSQuery(resp string) (COPSQuery, bool) {
	m := reCOPSQuery.FindStringSubmatch(resp)
	if len(m) < 2 {
		return COPSQuery{}, false
	}
	var q COPSQuery
	q.Mode, _ = strconv.Atoi(m[1])
	if m[2] != "" {
		q.Format, _ = strconv.Atoi(m[2])
	}
	if m[3] != "" {
		q.Oper = m[3]
		q.HasOper = true
	}
	if m[4] != "" {
		q.Act, _ = strconv.Atoi(m[4])
	}
	return q, true
}

// String возвращает человекочитаемое представление текущего оператора.
func (q COPSQuery) String() string {
	var mode string
	switch q.Mode {
	case 0:
		mode = "авто"
	case 1:
		mode = "ручной"
	case 2:
		mode = "дерегистрирован"
	case 3:
		mode = "только формат"
	case 4:
		mode = "авто/ручной"
	case 5:
		mode = "ручной/авто"
	default:
		mode = fmt.Sprintf("mode=%d", q.Mode)
	}
	if !q.HasOper {
		return fmt.Sprintf("оператор не выбран (%s)", mode)
	}
	name := q.Oper
	if q.Format == 2 {
		if known, ok := mccMncNames[q.Oper]; ok {
			name = fmt.Sprintf("%s [%s]", q.Oper, known)
		}
	}
	var act string
	switch q.Act {
	case 0:
		act = "GSM"
	case 2:
		act = "UTRAN"
	case 3:
		act = "EDGE"
	case 4:
		act = "HSDPA"
	case 5:
		act = "HSUPA"
	case 6:
		act = "HSPA"
	case 7:
		act = "LTE"
	default:
		act = fmt.Sprintf("act=%d", q.Act)
	}
	return fmt.Sprintf("%s (%s, %s)", name, mode, act)
}

// ==================== расшифровка списка операторов ====================

// mccMncNames — небольшая база соответствия MCC-MNC человекочитаемым
// именам операторов. Основное покрытие — Балтия + ближайшие страны.
var mccMncNames = map[string]string{
	// Латвия (MCC 247)
	"24701": "LMT (Latvia)",
	"24702": "Tele2 (Latvia)",
	"24703": "Triatel (Latvia)",
	"24704": "Latvijas Mobilais Telefons (Latvia)",
	"24705": "Bite (Latvia)",
	"24706": "Tele2 (Latvia, SIA)",
	"24707": "MTS (Latvia)",
	// Эстония (MCC 248)
	"24801": "Telia (Estonia)",
	"24802": "Elisa (Estonia)",
	"24803": "Tele2 (Estonia)",
	"24804": "Top Connect (Estonia)",
	// Литва (MCC 246)
	"24601": "Telia (Lithuania)",
	"24602": "Bite (Lithuania)",
	"24603": "Tele2 (Lithuania)",
	// Россия (MCC 250)
	"25001": "MTS (Russia)",
	"25002": "MegaFon (Russia)",
	"25020": "Tele2 (Russia)",
	"25099": "Beeline (Russia)",
	// Финляндия (MCC 244)
	"24403": "DNA (Finland)",
	"24405": "Elisa (Finland)",
	"24491": "Telia (Finland)",
}

// Operator — разобранная запись из списка +COPS=?.
type Operator struct {
	Status  int    // 0 unknown, 1 available, 2 current, 3 forbidden
	Name    string // длинное имя, как вернул модем
	Short   string // короткое имя
	MccMnc  string // MCC-MNC
	Known   string // человекочитаемое имя из базы (или "")
	Current bool   // статус == 2
}

// StatusText возвращает текстовое описание статуса.
func (o Operator) StatusText() string {
	switch o.Status {
	case 0:
		return "unknown"
	case 1:
		return "available"
	case 2:
		return "CURRENT (используется)"
	case 3:
		return "forbidden (запрещён для этой SIM)"
	}
	return fmt.Sprintf("status %d", o.Status)
}

// Display возвращает строку для отображения оператора.
func (o Operator) Display() string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s — %s", o.MccMnc, o.Name)
	if o.Known != "" && o.Known != o.Name {
		fmt.Fprintf(&b, " [%s]", o.Known)
	}
	fmt.Fprintf(&b, " — %s", o.StatusText())
	return b.String()
}

var reCOPSItem = regexp.MustCompile(`\((\d+),"([^"]*)","([^"]*)","([^"]*)"\)`)

// ParseCOPSList разбирает ответ AT+COPS=? на список операторов.
func ParseCOPSList(resp string) []Operator {
	matches := reCOPSItem.FindAllStringSubmatch(resp, -1)
	result := make([]Operator, 0, len(matches))
	for _, m := range matches {
		status, _ := strconv.Atoi(m[1])
		op := Operator{
			Status: status,
			Name:   m[2],
			Short:  m[3],
			MccMnc: m[4],
		}
		op.Current = status == 2
		if name, ok := mccMncNames[op.MccMnc]; ok {
			op.Known = name
		}
		result = append(result, op)
	}
	return result
}

// ==================== расшифровка ошибок CME / CMS ====================

// InterpretCMEError расшифровывает код +CME ERROR.
func InterpretCMEError(code int) string {
	switch code {
	case 0:
		return "phone failure"
	case 3:
		return "operation not allowed"
	case 10:
		return "SIM not inserted"
	case 11:
		return "SIM PIN required"
	case 12:
		return "SIM PUK required"
	case 13:
		return "SIM failure"
	case 14:
		return "SIM busy"
	case 15:
		return "SIM wrong"
	case 16:
		return "incorrect password"
	case 17:
		return "SIM PIN2 required"
	case 18:
		return "SIM PUK2 required"
	case 20:
		return "memory full"
	case 21:
		return "invalid index"
	case 22:
		return "not found"
	case 23:
		return "memory failure"
	case 24:
		return "text string too long"
	case 25:
		return "invalid characters in text string"
	case 26:
		return "dial string too long"
	case 27:
		return "invalid characters in dial string"
	case 30:
		return "no network service"
	case 31:
		return "network timeout"
	case 32:
		return "network not allowed — emergency calls only"
	case 40:
		return "network personalization PIN required"
	case 100:
		return "unknown (команда не поддерживается модемом)"
	}
	return fmt.Sprintf("неизвестный CME-код %d", code)
}

// InterpretCMSError расшифровывает код +CMS ERROR (для SMS).
func InterpretCMSError(code int) string {
	switch code {
	case 300:
		return "ME failure"
	case 301:
		return "SMS service of ME reserved"
	case 302:
		return "operation not allowed"
	case 303:
		return "operation not supported"
	case 304:
		return "invalid PDU mode parameter"
	case 305:
		return "invalid text mode parameter"
	case 310:
		return "SIM not inserted"
	case 311:
		return "SIM PIN required"
	case 312:
		return "PH-SIM PIN required"
	case 313:
		return "SIM failure"
	case 314:
		return "SIM busy"
	case 315:
		return "SIM wrong"
	case 320:
		return "memory failure"
	case 321:
		return "invalid memory index"
	case 322:
		return "memory full"
	case 330:
		return "SMSC address unknown"
	case 331:
		return "no network service"
	case 332:
		return "network timeout"
	case 500:
		return "unknown error (часто: нет баланса или SMS-центр недоступен)"
	}
	// Проприетарные коды SIMCom.
	switch code {
	case 2171:
		return "нет сети (SIMCom proprietary): модем не зарегистрирован в сети"
	case 2172:
		return "нет сети (SIMCom proprietary): сеть недоступна"
	}
	return fmt.Sprintf("неизвестный CMS-код %d", code)
}

// simcomTextErrors — проприетарные текстовые ответы SIM800,
// которые модем возвращает вместо числового кода.
var simcomTextErrors = map[string]string{
	"Temporary failure":        "временный сбой: SMSC недоступен или сеть занята (повторите через 1–5 минут)",
	"No network service":       "нет сети: модем не зарегистрирован в сети оператора",
	"Network not allowed":      "сеть не разрешена: SIM не имеет права работать в этой сети",
	"Unassigned number":        "номер не назначен: неверный номер получателя",
	"Operator barred":          "оператор заблокирован: проверьте баланс и настройки",
	"Memory capacity exceeded": "переполнена память SIM/модема для SMS",
	"Memory failure":           "ошибка памяти модема",
	"SIM failure":              "сбой SIM-карты",
}

// InterpretErrorLine пытается расшифровать строку вида "+CME ERROR: 10",
// "+CMS ERROR: 500" или текстовые ответы SIM800
// ("+CMS ERROR: Temporary failure").
func InterpretErrorLine(line string) string {
	line = strings.TrimSpace(line)

	for prefix, kind := range map[string]string{
		"+CMS ERROR:": "CMS",
		"+CME ERROR:": "CME",
	} {
		if !strings.HasPrefix(line, prefix) {
			continue
		}
		body := strings.TrimSpace(strings.TrimPrefix(line, prefix))

		// Числовой код?
		if code, err := strconv.Atoi(body); err == nil {
			if kind == "CMS" {
				return fmt.Sprintf("+CMS ERROR: %d — %s", code, InterpretCMSError(code))
			}
			return fmt.Sprintf("+CME ERROR: %d — %s", code, InterpretCMEError(code))
		}

		// Текстовый ответ SIM800?
		if interp, ok := simcomTextErrors[body]; ok {
			return fmt.Sprintf("%s %s — %s", prefix, body, interp)
		}
	}
	return line
}
