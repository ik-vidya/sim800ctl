// Package modem, файл report.go — сохранение, загрузка и сравнение
// диагностических отчётов в формате JSON.
//
// Использование в CLI:
//
//	sim800ctl -p /dev/ttyUSB0 diagnose --test1 --test2 --report today.json
//	sim800ctl -p /dev/ttyUSB0 diagnose --test1 --test2 --compare today.json
package modem

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"
)

// DiagnosticReport — сохраняемый отчёт диагностики.
type DiagnosticReport struct {
	Timestamp time.Time        `json:"timestamp"`
	Version   string           `json:"version,omitempty"`
	Port      string           `json:"port,omitempty"`
	Tests     []TestResultJSON `json:"tests"`
}

// TestResultJSON — сериализуемое представление TestResult.
type TestResultJSON struct {
	Name     string            `json:"name"`
	Passed   bool              `json:"passed"`
	Findings []string          `json:"findings,omitempty"`
	Hint     string            `json:"hint,omitempty"`
	Params   map[string]string `json:"params,omitempty"`
}

// NewReport создаёт отчёт из результатов тестов.
func NewReport(port, version string, results []TestResult) DiagnosticReport {
	r := DiagnosticReport{
		Timestamp: time.Now(),
		Version:   version,
		Port:      port,
	}
	for _, t := range results {
		r.Tests = append(r.Tests, TestResultJSON{
			Name:     t.Name,
			Passed:   t.Passed,
			Findings: t.Findings,
			Hint:     t.Hint,
			Params:   t.Params,
		})
	}
	return r
}

// Save сохраняет отчёт в файл path (JSON, отформатированный).
func (r DiagnosticReport) Save(path string) error {
	data, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return fmt.Errorf("маршалинг JSON: %w", err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("запись %s: %w", path, err)
	}
	return nil
}

// LoadReport читает отчёт из файла.
func LoadReport(path string) (DiagnosticReport, error) {
	var r DiagnosticReport
	data, err := os.ReadFile(path)
	if err != nil {
		return r, err
	}
	if err := json.Unmarshal(data, &r); err != nil {
		return r, fmt.Errorf("разбор %s: %w", path, err)
	}
	return r, nil
}

// CompareReports возвращает список человекочитаемых изменений между
// двумя отчётами. Сравниваются:
//   - флаг Passed по каждому тесту
//   - значения в Params по каждому ключу
//
// Порядок вывода — стабильный (сортировка по имени теста и ключу).
func CompareReports(oldR, newR DiagnosticReport) []string {
	var changes []string

	if !oldR.Timestamp.IsZero() && !newR.Timestamp.IsZero() {
		changes = append(changes, fmt.Sprintf("сравнение отчётов: %s → %s",
			oldR.Timestamp.Format("2006-01-02 15:04:05"),
			newR.Timestamp.Format("2006-01-02 15:04:05")))
	}

	oldBy := indexByName(oldR.Tests)
	newBy := indexByName(newR.Tests)

	names := unionKeys(oldBy, newBy)
	sort.Strings(names)

	for _, name := range names {
		oldT, oldOK := oldBy[name]
		newT, newOK := newBy[name]
		if !oldOK || !newOK {
			continue
		}
		if oldT.Passed != newT.Passed {
			changes = append(changes, fmt.Sprintf("%s: %s → %s",
				shortName(name),
				passedWord(oldT.Passed),
				passedWord(newT.Passed)))
		}
		keys := unionKeys(oldT.Params, newT.Params)
		sort.Strings(keys)
		for _, k := range keys {
			ov := oldT.Params[k]
			nv := newT.Params[k]
			if ov != nv {
				changes = append(changes, fmt.Sprintf("%s.%s: %q → %q",
					shortName(name), k, ov, nv))
			}
		}
	}

	return changes
}

// indexByName индексирует результаты по имени теста.
func indexByName(tests []TestResultJSON) map[string]TestResultJSON {
	m := make(map[string]TestResultJSON, len(tests))
	for _, t := range tests {
		m[t.Name] = t
	}
	return m
}

// unionKeys возвращает объединение ключей двух карт.
func unionKeys[V any](a, b map[string]V) []string {
	seen := make(map[string]bool, len(a)+len(b))
	for k := range a {
		seen[k] = true
	}
	for k := range b {
		seen[k] = true
	}
	out := make([]string, 0, len(seen))
	for k := range seen {
		out = append(out, k)
	}
	return out
}

// shortName вырезает короткое имя теста из полного названия:
// "test1 — ОСМОТР: идентификация" → "test1".
func shortName(full string) string {
	if idx := strings.IndexAny(full, " —-"); idx > 0 {
		return full[:idx]
	}
	return full
}

// passedWord возвращает русское слово для флага Passed.
func passedWord(p bool) string {
	if p {
		return "УСПЕХ"
	}
	return "ПРОБЛЕМА"
}
