// Пакет pdu — кодирование SMS в формат PDU (3GPP TS 23.040 / 23.038).
//
// Реализация сознательно не тянет внешних зависимостей и повторяет
// проверенную логику оригинальной C#-программы.
package pdu

import (
	"encoding/hex"
	"fmt"
	"strings"
	"time"
)

// Part — одна часть SMS (одно SMS в терминах модема).
type Part struct {
	// Hex — PDU в hex-формате, включая поле SMSC (первый байт = 0x00,
	// что означает «использовать SMSC из SIM-карты»).
	Hex string
	// TpduLength — длина TPDU без октета SMSC — значение для AT+CMGS=<len>.
	TpduLength int
	// PartNum — номер части (1-based).
	PartNum int
	// TotalParts — всего частей в этом сообщении.
	TotalParts int
	// Encoding — "GSM-7" или "UCS-2", для информации.
	Encoding string
	// Chars — количество символов пользователя.
	Chars int
}

// gsm7Basic — базовая таблица GSM 7-bit (индекс = значение септета).
// Символ 0x1B зарезервирован для escape-последовательности.
const gsm7Basic = "@£$¥èéùìòÇ\nØø\rÅå" +
	"Δ_ΦΓΛΩΠΨΣΘΞ\x1BÆæßÉ" +
	" !\"#¤%&'()*+,-./0123456789:;<=>?" +
	"¡ABCDEFGHIJKLMNOPQRSTUVWXYZÄÖÑÜ§" +
	"¿abcdefghijklmnopqrstuvwxyzäöñüà"

// gsm7Ext — расширенная таблица (escape 0x1B + второй септет).
var gsm7Ext = map[rune]byte{
	'\f': 0x0A, '^': 0x14, '{': 0x28, '}': 0x29, '\\': 0x2F,
	'[': 0x3C, '~': 0x3D, ']': 0x3E, '|': 0x40, '€': 0x65,
}

// buildBasicIndex строит обратный индекс для базовой таблицы.
var gsm7BasicIdx = func() map[rune]byte {
	m := make(map[rune]byte, len(gsm7Basic))
	for i, r := range gsm7Basic {
		if i == 0x1B {
			continue
		}
		m[r] = byte(i)
	}
	return m
}()

// BuildOptions — опции сборки PDU.
type BuildOptions struct {
	// ForceUCS2 — всегда использовать UCS-2, даже если текст
	// полностью укладывается в GSM-7. Обходит баг SIM800L: модем
	// некорректно конвертирует GSM-7 сообщения при чтении в text mode
	// с CSCS=UCS2 (возвращает искажённые символы). UCS-2 сообщения
	// читаются правильно.
	ForceUCS2 bool
	// RequestDR — установить бит TP-SRR в первом октете SMS-SUBMIT,
	// запрашивая delivery report от SMSC.
	RequestDR bool
}

// Build кодирует текст в список PDU (автовыбор кодировки).
func Build(number, text string) ([]Part, error) {
	return BuildWith(number, text, BuildOptions{})
}

// BuildWith кодирует текст в список PDU с указанными опциями.
func BuildWith(number, text string, opts BuildOptions) ([]Part, error) {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	if strings.TrimSpace(text) == "" {
		return nil, fmt.Errorf("текст сообщения пуст")
	}

	da, err := encodeAddress(number)
	if err != nil {
		return nil, err
	}

	// Reference для multipart. В CLI-версии достаточно псевдослучайного
	// значения; коллизии при одиночной отправке практически исключены.
	ref := byte(time.Now().UnixNano() & 0xFF)

	if !opts.ForceUCS2 {
		if septets := toGsm7(text); septets != nil {
			return buildGsm7(da, ref, septets, opts)
		}
	}
	return buildUcs2(da, ref, text, opts)
}

// Describe возвращает краткое описание текста с автовыбором кодировки.
func Describe(text string) string {
	return DescribeWith(text, BuildOptions{})
}

// DescribeWith возвращает краткое описание текста с учётом опций.
// Если opts.ForceUCS2 = true, сообщает, что кодировка — UCS-2,
// независимо от того, что текст мог бы уложиться в GSM-7.
func DescribeWith(text string, opts BuildOptions) string {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	if text == "" {
		return "текст пуст"
	}
	if opts.ForceUCS2 {
		parts := countUcs2Parts(text)
		return fmt.Sprintf("UCS-2 (принудительно): %d символов, частей: %d (до 70 в одиночном, по 67 в составном)",
			len([]rune(text)), parts)
	}
	if septets := toGsm7(text); septets != nil {
		parts := countGsm7Parts(septets)
		return fmt.Sprintf("GSM-7 (латиница): %d символов, частей: %d (до 160 в одиночном, по 153 в составном)",
			septetsLen(septets), parts)
	}
	parts := countUcs2Parts(text)
	return fmt.Sprintf("UCS-2 (кириллица/Unicode): %d символов, частей: %d (до 70 в одиночном, по 67 в составном)",
		len([]rune(text)), parts)
}

// ======================== кодирование адреса ========================

// encodeAddress кодирует номер получателя в формат TP-DA.
//
// Формат: <len:1> <toa:1> <digits-bcd>
//   - len — число цифр в номере (без «+»);
//   - toa — 0x91 для международного, 0x81 для национального;
//   - digits — каждая пара цифр в обратном порядке; при нечётной длине
//     добавляется F.
func encodeAddress(number string) (string, error) {
	international := strings.HasPrefix(number, "+")
	digits := number
	if international {
		digits = digits[1:]
	}
	if len(digits) < 3 || len(digits) > 20 {
		return "", fmt.Errorf("некорректная длина номера: %d", len(digits))
	}
	for _, r := range digits {
		if r < '0' || r > '9' {
			return "", fmt.Errorf("номер содержит недопустимый символ %q", r)
		}
	}

	padded := digits
	if len(padded)%2 == 1 {
		padded += "F"
	}

	var b strings.Builder
	fmt.Fprintf(&b, "%02X", len(digits))
	if international {
		b.WriteString("91")
	} else {
		b.WriteString("81")
	}
	for i := 0; i < len(padded); i += 2 {
		b.WriteByte(padded[i+1])
		b.WriteByte(padded[i])
	}
	return b.String(), nil
}

// ======================== GSM-7 ========================

// toGsm7 пытается закодировать текст в GSM-7. Возвращает nil, если
// хотя бы один символ не входит в таблицу (тогда нужен UCS-2).
// Каждый символ представлен одним или двумя септетами.
func toGsm7(text string) [][]byte {
	out := make([][]byte, 0, len(text))
	for _, r := range text {
		if b, ok := gsm7BasicIdx[r]; ok {
			out = append(out, []byte{b})
			continue
		}
		if b, ok := gsm7Ext[r]; ok {
			out = append(out, []byte{0x1B, b})
			continue
		}
		return nil
	}
	return out
}

// septetsLen считает общее число септетов.
func septetsLen(chars [][]byte) int {
	n := 0
	for _, c := range chars {
		n += len(c)
	}
	return n
}

// splitGsm7 разбивает последовательность септетов на части.
// Escape-пара не разрывается между частями.
func splitGsm7(chars [][]byte) [][]byte {
	total := septetsLen(chars)
	limit := 160
	if total > 160 {
		limit = 153
	}
	var chunks [][]byte
	var cur []byte
	for _, c := range chars {
		if len(cur)+len(c) > limit {
			chunks = append(chunks, cur)
			cur = nil
		}
		cur = append(cur, c...)
	}
	if len(cur) > 0 {
		chunks = append(chunks, cur)
	}
	return chunks
}

// countGsm7Parts возвращает число частей для текста.
func countGsm7Parts(chars [][]byte) int {
	total := septetsLen(chars)
	if total <= 160 {
		return 1
	}
	// ceil(total / 153)
	n := total / 153
	if total%153 != 0 {
		n++
	}
	return n
}

// packSeptets упаковывает септеты в байты.
// fillBits — число бит-заполнителей перед первым септетом (для multipart).
func packSeptets(septets []byte, fillBits int) []byte {
	totalBits := fillBits + len(septets)*7
	out := make([]byte, (totalBits+7)/8)
	bit := fillBits
	for _, s := range septets {
		for i := 0; i < 7; i++ {
			if (s>>i)&1 != 0 {
				out[bit/8] |= 1 << (bit % 8)
			}
			bit++
		}
	}
	return out
}

// buildGsm7 собирает PDU для GSM-7 текста.
func buildGsm7(da string, ref byte, septets [][]byte, opts BuildOptions) ([]Part, error) {
	chunks := splitGsm7(septets)
	if len(chunks) > 255 {
		return nil, fmt.Errorf("слишком много частей: %d", len(chunks))
	}
	total := len(chunks)
	parts := make([]Part, 0, total)
	for i, chunk := range chunks {
		var (
			udh  []byte
			body []byte
			udl  int
		)
		if total == 1 {
			body = packSeptets(chunk, 0)
			udl = len(chunk)
		} else {
			udh = udhHeader(ref, total, i+1)
			body = packSeptets(chunk, 1)
			udl = 7 + len(chunk)
		}
		p := makePdu(da, 0x00, udh, body, udl, opts.RequestDR)
		p.PartNum = i + 1
		p.TotalParts = total
		p.Encoding = "GSM-7"
		p.Chars = len(chunk)
		parts = append(parts, p)
	}
	return parts, nil
}

// ======================== UCS-2 ========================

// splitUcs2 разбивает текст на части, не разрывая суррогатные пары.
func splitUcs2(text string) []string {
	runes := []rune(text)
	limit := 70
	if len(runes) > 70 {
		limit = 67
	}
	var out []string
	for i := 0; i < len(runes); {
		end := i + limit
		if end > len(runes) {
			end = len(runes)
		}
		out = append(out, string(runes[i:end]))
		i = end
	}
	return out
}

// countUcs2Parts возвращает число частей для UCS-2 текста.
func countUcs2Parts(text string) int {
	n := len([]rune(text))
	if n <= 70 {
		return 1
	}
	parts := n / 67
	if n%67 != 0 {
		parts++
	}
	return parts
}

// encodeUcs2 — UTF-16BE кодирование.
func encodeUcs2(s string) []byte {
	runes := []rune(s)
	out := make([]byte, 0, len(runes)*2)
	for _, r := range runes {
		if r > 0xFFFF {
			// Суррогатная пара.
			r -= 0x10000
			hi := 0xD800 + (r >> 10)
			lo := 0xDC00 + (r & 0x3FF)
			out = append(out, byte(hi>>8), byte(hi), byte(lo>>8), byte(lo))
		} else {
			out = append(out, byte(r>>8), byte(r))
		}
	}
	return out
}

// buildUcs2 собирает PDU для UCS-2 текста.
func buildUcs2(da string, ref byte, text string, opts BuildOptions) ([]Part, error) {
	chunks := splitUcs2(text)
	if len(chunks) > 255 {
		return nil, fmt.Errorf("слишком много частей: %d", len(chunks))
	}
	total := len(chunks)
	parts := make([]Part, 0, total)
	for i, chunk := range chunks {
		body := encodeUcs2(chunk)
		var (
			udh []byte
			udl int
		)
		if total == 1 {
			udl = len(body)
		} else {
			udh = udhHeader(ref, total, i+1)
			udl = 6 + len(body)
		}
		p := makePdu(da, 0x08, udh, body, udl, opts.RequestDR)
		p.PartNum = i + 1
		p.TotalParts = total
		p.Encoding = "UCS-2"
		p.Chars = len([]rune(chunk))
		parts = append(parts, p)
	}
	return parts, nil
}

// ======================== сборка PDU ========================

// udhHeader формирует UDH для multipart: 05 00 03 <ref> <total> <seq>.
func udhHeader(ref byte, total, seq int) []byte {
	return []byte{0x05, 0x00, 0x03, ref, byte(total), byte(seq)}
}

// makePdu собирает PDU SMS-SUBMIT.
//
// Первый октет:
//
//	0x11 — SMS-SUBMIT, VPF=01 (относительный VP), без UDHI
//	0x51 — то же, но с UDHI (multipart)
//
// SMSC всегда 0x00 — «использовать SMSC из SIM-карты».
// Это то же, что делает оригинальная C#-программа.
func makePdu(da string, dcs byte, udh, body []byte, udl int, requestDR bool) Part {
	var sb strings.Builder
	sb.WriteString("00") // SMSC: длина 0, использовать SIM
	var first byte
	if udh == nil {
		first = 0x11
	} else {
		first = 0x51
	}
	if requestDR {
		first |= 0x20 // TP-SRR: запросить delivery report
	}
	fmt.Fprintf(&sb, "%02X", first)
	sb.WriteString("00") // TP-MR (заполнит модем)
	sb.WriteString(da)
	sb.WriteString("00")          // TP-PID
	fmt.Fprintf(&sb, "%02X", dcs) // TP-DCS
	sb.WriteString("AA")          // TP-VP: 4 дня
	fmt.Fprintf(&sb, "%02X", udl) // TP-UDL
	if udh != nil {
		sb.WriteString(hex.EncodeToString(udh))
	}
	sb.WriteString(hex.EncodeToString(body))

	full := strings.ToUpper(sb.String())
	return Part{
		Hex:        full,
		TpduLength: len(full)/2 - 1, // минус октет SMSC
	}
}
