// Package pdu, файл decode.go — декодер PDU (SMS-DELIVER,
// SMS-SUBMIT, SMS-STATUS-REPORT). Используется при чтении входящих
// SMS в PDU-режиме: он корректно разбирает и GSM-7, и UCS-2, обходя
// известный баг SIM800L с конвертацией GSM-7 → UCS-2 при чтении в
// текстовом режиме.
package pdu

import (
	"encoding/hex"
	"fmt"
	"strings"
	"time"
)

// DecodedSMS — результат декодирования PDU.
type DecodedSMS struct {
	Type      string // "deliver", "submit", "status-report"
	MTI       int
	SMSC      string
	Peer      string // для deliver — отправитель, для submit — получатель, для status-report — адресат
	DCS       byte
	Encoding  string // "GSM-7" или "UCS-2"
	Timestamp time.Time
	Text      string
	UDH       []byte
	Multipart *MultipartInfo
	Reference int // TP-MR
	Status    int // TP-ST (для status-report)
	PID       byte
}

// MultipartInfo — параметры multipart-сообщения из UDH.
type MultipartInfo struct {
	Reference int
	Total     int
	Sequence  int
}

// Decode разбирает hex-PDU и возвращает результат.
func Decode(hexPdu string) (DecodedSMS, error) {
	b, err := hex.DecodeString(strings.TrimSpace(hexPdu))
	if err != nil {
		return DecodedSMS{}, fmt.Errorf("не hex: %w", err)
	}
	if len(b) < 2 {
		return DecodedSMS{}, fmt.Errorf("слишком короткий PDU: %d байт", len(b))
	}

	var d DecodedSMS
	p := 0

	// SMSC
	smscLen := int(b[p])
	p++
	if smscLen > 0 {
		if p+smscLen > len(b) {
			return DecodedSMS{}, fmt.Errorf("SMSC выходит за границы")
		}
		d.SMSC = decodeAddressBCD(b[p:p+smscLen], -1)
		p += smscLen
	}
	if p >= len(b) {
		return DecodedSMS{}, fmt.Errorf("нет первого октета")
	}

	first := b[p]
	p++
	d.MTI = int(first) & 0x03

	switch d.MTI {
	case 0:
		d.Type = "deliver"
		return decodeDeliver(b, p, d, first)
	case 1:
		d.Type = "submit"
		return decodeSubmit(b, p, d, first)
	case 2:
		d.Type = "status-report"
		return decodeStatusReport(b, p, d)
	}
	return DecodedSMS{}, fmt.Errorf("неподдерживаемый MTI: %d", d.MTI)
}

func decodeDeliver(b []byte, p int, d DecodedSMS, first byte) (DecodedSMS, error) {
	udhi := first&0x40 != 0

	// OA
	if p >= len(b) {
		return d, fmt.Errorf("нет OA")
	}
	oaLen := int(b[p])
	p++
	if oaLen > 0 {
		// ToA + BCD digits
		need := 1 + (oaLen+1)/2
		if p+need > len(b) {
			return d, fmt.Errorf("OA выходит за границы")
		}
		d.Peer = decodeAddressBCD(b[p:p+need], oaLen)
		p += need
	}

	// PID, DCS
	if p+2 > len(b) {
		return d, fmt.Errorf("нет PID/DCS")
	}
	d.PID = b[p]
	d.DCS = b[p+1]
	d.Encoding = encodingFromDCS(d.DCS)
	p += 2

	// SCTS
	if p+7 > len(b) {
		return d, fmt.Errorf("нет SCTS")
	}
	d.Timestamp = decodeSCTS(b[p : p+7])
	p += 7

	// UDL + UD
	if p >= len(b) {
		return d, fmt.Errorf("нет UDL")
	}
	udl := int(b[p])
	p++
	udLen := udl
	if d.Encoding == "GSM-7" {
		udLen = (udl*7 + 7) / 8
	}
	if p+udLen > len(b) {
		udLen = len(b) - p
	}
	return decodeUD(d, b[p:p+udLen], udhi)
}

func decodeSubmit(b []byte, p int, d DecodedSMS, first byte) (DecodedSMS, error) {
	udhi := first&0x40 != 0
	vpf := (first >> 3) & 0x03

	// MR
	if p >= len(b) {
		return d, fmt.Errorf("нет MR")
	}
	d.Reference = int(b[p])
	p++

	// DA
	if p >= len(b) {
		return d, fmt.Errorf("нет DA")
	}
	daLen := int(b[p])
	p++
	if daLen > 0 {
		need := 1 + (daLen+1)/2
		if p+need > len(b) {
			return d, fmt.Errorf("DA выходит за границы")
		}
		d.Peer = decodeAddressBCD(b[p:p+need], daLen)
		p += need
	}

	// PID, DCS
	if p+2 > len(b) {
		return d, fmt.Errorf("нет PID/DCS")
	}
	d.PID = b[p]
	d.DCS = b[p+1]
	d.Encoding = encodingFromDCS(d.DCS)
	p += 2

	// VP
	switch vpf {
	case 0x01:
		p++
	case 0x02, 0x03:
		p += 7
	}

	// UDL + UD
	if p >= len(b) {
		return d, fmt.Errorf("нет UDL")
	}
	udl := int(b[p])
	p++
	udLen := udl
	if d.Encoding == "GSM-7" {
		udLen = (udl*7 + 7) / 8
	}
	if p+udLen > len(b) {
		udLen = len(b) - p
	}
	return decodeUD(d, b[p:p+udLen], udhi)
}

func decodeStatusReport(b []byte, p int, d DecodedSMS) (DecodedSMS, error) {
	// MR
	if p >= len(b) {
		return d, fmt.Errorf("нет MR")
	}
	d.Reference = int(b[p])
	p++

	// RA
	if p >= len(b) {
		return d, fmt.Errorf("нет RA")
	}
	raLen := int(b[p])
	p++
	if raLen > 0 {
		need := 1 + (raLen+1)/2
		if p+need > len(b) {
			return d, fmt.Errorf("RA выходит за границы")
		}
		d.Peer = decodeAddressBCD(b[p:p+need], raLen)
		p += need
	}

	// SCTS
	if p+7 > len(b) {
		return d, fmt.Errorf("нет SCTS")
	}
	d.Timestamp = decodeSCTS(b[p : p+7])
	p += 7

	// DT
	if p+7 > len(b) {
		return d, fmt.Errorf("нет DT")
	}
	p += 7

	// ST
	if p >= len(b) {
		return d, fmt.Errorf("нет ST")
	}
	d.Status = int(b[p])
	d.Text = interpretStatus(d.Status)
	return d, nil
}

func decodeUD(d DecodedSMS, ud []byte, udhi bool) (DecodedSMS, error) {
	payload := ud
	if udhi && len(ud) > 0 {
		udhLen := int(ud[0]) + 1
		if udhLen <= len(ud) {
			d.UDH = ud[:udhLen]
			payload = ud[udhLen:]
			if info, ok := parseMultipartUDH(d.UDH); ok {
				d.Multipart = &info
			}
		}
	}

	switch d.Encoding {
	case "GSM-7":
		fillBits := 0
		if udhi {
			bitsUsed := len(d.UDH) * 8
			fillBits = (7 - bitsUsed%7) % 7
		}
		d.Text = decodeGSM7(payload, fillBits)
	case "UCS-2":
		d.Text = decodeUCS2(payload)
	default:
		d.Text = hex.EncodeToString(payload)
	}
	return d, nil
}

// ==================== служебные декодеры ====================

func encodingFromDCS(dcs byte) string {
	// Общий случай: DCS 0x00..0x0F (general data coding).
	// 0x00..0x03 — GSM-7; 0x08..0x0B — UCS-2; 0x04..0x07 — 8bit.
	switch dcs & 0x0C {
	case 0x08:
		return "UCS-2"
	case 0x04:
		return "8bit"
	}
	return "GSM-7"
}

// decodeAddressBCD декодирует ToA + BCD-цифры в строку номера.
// Если n < 0 — длина определяется по BCD (последняя полубайта F — отбрасывается).
func decodeAddressBCD(b []byte, n int) string {
	if len(b) < 1 {
		return ""
	}
	toa := b[0]
	digits := b[1:]
	ton := (toa >> 4) & 0x07
	if ton == 5 {
		// alphanumeric address — GSM-7
		return decodeGSM7(digits, 0)
	}

	var sb strings.Builder
	international := toa&0x70 == 0x10
	if international {
		sb.WriteByte('+')
	}
	maxDigits := len(digits) * 2
	if n >= 0 && n < maxDigits {
		maxDigits = n
	}
	for i := 0; i < len(digits) && sb.Len() < maxDigits+1; i++ {
		lo := digits[i] & 0x0F
		hi := (digits[i] >> 4) & 0x0F
		if lo != 0x0F && sb.Len() < maxDigits+1 {
			sb.WriteByte("0123456789ABCDEF"[lo])
		}
		if hi != 0x0F && sb.Len() < maxDigits+1 {
			sb.WriteByte("0123456789ABCDEF"[hi])
		}
	}
	return sb.String()
}

// decodeSCTS декодирует timestamp из 7 байт SCTS (Service Centre Time
// Stamp). Формат: YY MM DD HH MM SS TZ, все — semi-octets (BCD) с
// обратным порядком полубайтов.
//
// Timezone: значение — в четвертях часа. Пример: 0x21 → «12» после
// обратного swap → 12 четвертей = 3 часа. Знак: бит 0x08 старшего
// полубайта (то есть бит 0x80 исходного байта).
func decodeSCTS(b []byte) time.Time {
	if len(b) < 7 {
		return time.Time{}
	}
	swap := func(x byte) int {
		return int((x&0x0F)*10 + ((x >> 4) & 0x0F))
	}
	year := swap(b[0])
	month := swap(b[1])
	day := swap(b[2])
	hour := swap(b[3])
	min := swap(b[4])
	sec := swap(b[5])

	// Timezone.
	tzByte := int(b[6])
	sign := 1
	if tzByte&0x80 != 0 {
		sign = -1
	}
	tzVal := tzByte & 0x7F
	lo := tzVal & 0x0F
	hi := (tzVal >> 4) & 0x07
	quarterHours := lo*10 + hi
	tzMinutes := sign * quarterHours * 15

	if year < 90 {
		year += 2000
	} else {
		year += 1900
	}
	loc := time.FixedZone("", tzMinutes*60)
	return time.Date(year, time.Month(month), day, hour, min, sec, 0, loc)
}

func parseMultipartUDH(udh []byte) (MultipartInfo, bool) {
	if len(udh) < 2 {
		return MultipartInfo{}, false
	}
	p := 1
	for p+2 <= len(udh) {
		iei := udh[p]
		iedl := int(udh[p+1])
		if p+2+iedl > len(udh) {
			break
		}
		if iei == 0x00 && iedl == 3 {
			return MultipartInfo{
				Reference: int(udh[p+2]),
				Total:     int(udh[p+3]),
				Sequence:  int(udh[p+4]),
			}, true
		}
		if iei == 0x08 && iedl == 4 {
			return MultipartInfo{
				Reference: int(udh[p+2])<<8 | int(udh[p+3]),
				Total:     int(udh[p+4]),
				Sequence:  int(udh[p+5]),
			}, true
		}
		p += 2 + iedl
	}
	return MultipartInfo{}, false
}

// decodeGSM7 распаковывает septets в строку. fillBits — сколько бит
// пропустить в начале (для multipart, после UDH).
func decodeGSM7(data []byte, fillBits int) string {
	if len(data) == 0 {
		return ""
	}
	totalBits := len(data) * 8
	var sb strings.Builder
	bitPos := fillBits

	for bitPos+7 <= totalBits {
		var septet byte
		for i := 0; i < 7; i++ {
			bp := bitPos + i
			if data[bp/8]&(1<<(bp%8)) != 0 {
				septet |= 1 << i
			}
		}
		if septet == 0x1B {
			bitPos += 7
			if bitPos+7 > totalBits {
				break
			}
			var next byte
			for i := 0; i < 7; i++ {
				bp := bitPos + i
				if data[bp/8]&(1<<(bp%8)) != 0 {
					next |= 1 << i
				}
			}
			sb.WriteRune(gsm7ExtDecode(next))
			bitPos += 7
			continue
		}
		sb.WriteRune(gsm7BasicDecode(septet))
		bitPos += 7
	}
	return sb.String()
}

// gsm7BasicRunes — руны базовой таблицы, индекс = значение септета.
var gsm7BasicRunes = []rune(gsm7Basic)

func gsm7BasicDecode(septet byte) rune {
	if int(septet) < len(gsm7BasicRunes) {
		return gsm7BasicRunes[septet]
	}
	return '?'
}

func gsm7ExtDecode(septet byte) rune {
	for r, s := range gsm7Ext {
		if s == septet {
			return r
		}
	}
	return '?'
}

func decodeUCS2(data []byte) string {
	if len(data)%2 != 0 {
		data = data[:len(data)-1]
	}
	var sb strings.Builder
	for i := 0; i < len(data); i += 2 {
		unit := uint16(data[i])<<8 | uint16(data[i+1])
		if unit >= 0xD800 && unit <= 0xDBFF && i+3 < len(data) {
			next := uint16(data[i+2])<<8 | uint16(data[i+3])
			if next >= 0xDC00 && next <= 0xDFFF {
				r := rune(unit-0xD800)<<10 | rune(next-0xDC00) + 0x10000
				sb.WriteRune(r)
				i += 2
				continue
			}
		}
		sb.WriteRune(rune(unit))
	}
	return sb.String()
}

// interpretStatus — расшифровка TP-ST (3GPP TS 23.040 §9.2.3.15).
func interpretStatus(st int) string {
	switch st {
	case 0x00:
		return "доставлено"
	case 0x01:
		return "получено SMSC"
	case 0x02:
		return "заменено"
	case 0x20:
		return "не доставлено (нет абонента)"
	case 0x21:
		return "не доставлено (занято)"
	case 0x22:
		return "не доставлено (нет ответа)"
	case 0x40:
		return "ошибка SMSC"
	}
	return fmt.Sprintf("неизвестный статус 0x%02X", st)
}
