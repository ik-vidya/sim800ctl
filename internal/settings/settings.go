// Пакет settings — сохранение настроек в кроссплатформенном каталоге.
package settings

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// MaxNumbers — сколько последних номеров хранить.
const MaxNumbers = 5

// Settings — сохраняемые настройки.
type Settings struct {
	Port    string   `json:"port"`
	Baud    int      `json:"baud"`
	Numbers []string `json:"numbers"`
}

// Dir возвращает кроссплатформенный каталог настроек:
//
//   - Linux:   ~/.config/sim800ctl
//   - Windows: %APPDATA%\sim800ctl
//   - macOS:   ~/Library/Application Support/sim800ctl
//
// Если получить каталог не удалось, используется папка рядом с бинарником.
func Dir() string {
	base, err := os.UserConfigDir()
	if err != nil {
		if exe, err2 := os.Executable(); err2 == nil {
			return filepath.Dir(exe)
		}
		return "."
	}
	return filepath.Join(base, "sim800ctl")
}

// Path возвращает путь к файлу настроек.
func Path() string {
	return filepath.Join(Dir(), "settings.json")
}

// Load читает настройки из файла. Если файла нет или он повреждён —
// возвращает значения по умолчанию (baud=115200).
func Load() Settings {
	var s Settings
	data, err := os.ReadFile(Path())
	if err == nil {
		_ = json.Unmarshal(data, &s)
	}
	if s.Baud == 0 {
		s.Baud = 115200
	}
	return s
}

// Save записывает настройки. Ошибки записи игнорируются: файл настроек
// не критичен для работы.
func Save(s Settings) error {
	if err := os.MkdirAll(Dir(), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(Path(), data, 0o644)
}

// Remember добавляет номер в начало списка и обрезает историю
// до MaxNumbers.
func (s *Settings) Remember(number string) {
	if number == "" {
		return
	}
	// Убираем дубликат, если он уже был.
	out := s.Numbers[:0]
	for _, n := range s.Numbers {
		if n != number {
			out = append(out, n)
		}
	}
	s.Numbers = append([]string{number}, out...)
	if len(s.Numbers) > MaxNumbers {
		s.Numbers = s.Numbers[:MaxNumbers]
	}
}
