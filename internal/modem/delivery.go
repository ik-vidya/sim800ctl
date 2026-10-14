// Package modem, файл delivery.go — ожидание отчёта о доставке
// (delivery report, URC +CDS).
package modem

import (
	"fmt"
	"strings"
	"time"

	"github.com/ik-vidya/sim800ctl/pkg/pdu"
)

// DeliveryReport — разобранный отчёт о доставке.
type DeliveryReport struct {
	Reference  int
	Status     int
	StatusText string
	Peer       string
	Timestamp  time.Time
	Raw        string
}

// WaitForDeliveryReport ожидает появления URC +CDS с указанным reference.
// Если ref пуст — принимает любой первый отчёт.
//
// Модем отправляет +CDS через 5–30 секунд после отправки. Если за
// timeout ничего не пришло — возвращается ошибка.
func (m *Modem) WaitForDeliveryReport(ref string, timeout time.Duration) (DeliveryReport, error) {
	m.log(fmt.Sprintf("Ожидаю delivery report (ref=%s, timeout=%s)...", ref, timeout))
	deadline := time.Now().Add(timeout)

	for time.Now().Before(deadline) {
		m.readAvailable()
		line := m.readLine()
		if line == "" {
			time.Sleep(200 * time.Millisecond)
			continue
		}
		m.log("<< " + line)

		if !strings.HasPrefix(line, "+CDS:") {
			continue
		}
		// Далее идёт строка с PDU.
		var pduLine string
		for i := 0; i < 5; i++ {
			m.readAvailable()
			pduLine = m.readLine()
			if pduLine != "" {
				break
			}
			time.Sleep(100 * time.Millisecond)
		}
		if pduLine == "" {
			continue
		}
		m.log("<< " + pduLine)

		dr, err := parseDeliveryReport(pduLine)
		if err != nil {
			m.log("не удалось разобрать +CDS: " + err.Error())
			continue
		}
		if ref != "" && fmt.Sprintf("%d", dr.Reference) != ref {
			m.log(fmt.Sprintf("CDS для ref=%d, ждём %s — игнорирую",
				dr.Reference, ref))
			continue
		}
		return dr, nil
	}
	return DeliveryReport{}, fmt.Errorf("delivery report не получен за %s", timeout)
}

func parseDeliveryReport(hexPdu string) (DeliveryReport, error) {
	d, err := pdu.Decode(hexPdu)
	if err != nil {
		return DeliveryReport{}, err
	}
	if d.Type != "status-report" {
		return DeliveryReport{}, fmt.Errorf("не status-report: %s", d.Type)
	}
	return DeliveryReport{
		Reference:  d.Reference,
		Status:     d.Status,
		StatusText: d.Text,
		Peer:       d.Peer,
		Timestamp:  d.Timestamp,
		Raw:        hexPdu,
	}, nil
}
