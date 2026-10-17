package modem

import (
	"strings"
	"testing"
)

// ==================== readLine: терминаторы ====================
//
// readLine должен принимать и \r, и \n как терминатор строки.
// SIM800 при сбое (переполнение приёмного буфера, +CME ERROR:
// SIM busy) шлёт строки без \n. Раньше readLine искал только \n,
// возвращал "", а drain терял обрезанную строку (симптом
// "<< +CME ER" в логе).

func TestReadLineLF(t *testing.T) {
	m := &Modem{buf: []byte("OK\n")}
	if got := m.readLine(); got != "OK" {
		t.Errorf("readLine=%q, ожидалось %q", got, "OK")
	}
}

func TestReadLineCRLF(t *testing.T) {
	m := &Modem{buf: []byte("OK\r\n")}
	if got := m.readLine(); got != "OK" {
		t.Errorf("readLine=%q, ожидалось %q", got, "OK")
	}
}

func TestReadLineBareCR(t *testing.T) {
	// Ключевой кейс из реального лога: SIM800 прислал ответ
	// без \n. Раньше строка терялась.
	m := &Modem{buf: []byte("+CME ERROR: SIM busy\r")}
	if got := m.readLine(); got != "+CME ERROR: SIM busy" {
		t.Errorf("readLine=%q, ожидалось %q",
			got, "+CME ERROR: SIM busy")
	}
}

func TestReadLineIncomplete(t *testing.T) {
	// Нет терминатора — пустая строка, буфер не тронут.
	m := &Modem{buf: []byte("+CME ER")}
	if got := m.readLine(); got != "" {
		t.Errorf("readLine=%q, ожидалось пусто", got)
	}
	if string(m.buf) != "+CME ER" {
		t.Errorf("буфер изменён: %q", m.buf)
	}
}

func TestReadLineSkipEmpty(t *testing.T) {
	// Пустые строки (после \r\n) пропускаются, не возвращаются.
	m := &Modem{buf: []byte("\r\n\r\nOK\r\n")}
	if got := m.readLine(); got != "OK" {
		t.Errorf("readLine=%q, ожидалось %q", got, "OK")
	}
	if len(m.buf) != 0 {
		t.Errorf("буфер не пуст: %q", m.buf)
	}
}

func TestReadLineMultiple(t *testing.T) {
	m := &Modem{buf: []byte("+CSQ: 17,0\r\nOK\r\n")}
	got1 := m.readLine()
	got2 := m.readLine()
	if got1 != "+CSQ: 17,0" || got2 != "OK" {
		t.Errorf("readLine #1=%q, #2=%q", got1, got2)
	}
}

func TestReadLineConsecutiveCR(t *testing.T) {
	// Две строки через \r без \n — обе должны вернуться.
	m := &Modem{buf: []byte("+CMTI: \"SM\",5\r+CSQ: 17,0\r")}
	got1 := m.readLine()
	got2 := m.readLine()
	if got1 != `+CMTI: "SM",5` {
		t.Errorf("readLine #1=%q", got1)
	}
	if got2 != "+CSQ: 17,0" {
		t.Errorf("readLine #2=%q", got2)
	}
}

// ==================== TestDescription ====================

func TestTestDescriptionTest5Has180(t *testing.T) {
	// Регрессионный тест: раньше было «до 60 с». Разработчик
	// модема указал, что AT+COPS=? на SIM800 занимает до 2-3
	// минут; тест5 должен упоминать 180.
	d := TestDescription("test5")
	if !strings.Contains(d, "180") {
		t.Errorf("TestDescription(\"test5\")=%q, ожидалось упоминание 180",
			d)
	}
	if strings.Contains(d, "60") {
		t.Errorf("TestDescription(\"test5\") всё ещё содержит 60: %q", d)
	}
}
