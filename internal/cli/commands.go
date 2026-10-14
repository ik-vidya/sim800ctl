// Пакет cli — команды командной строки на основе cobra.
package cli

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"go.bug.st/serial/enumerator"

	"github.com/ik-vidya/sim800ctl/internal/modem"
	"github.com/ik-vidya/sim800ctl/internal/settings"
	"github.com/ik-vidya/sim800ctl/pkg/number"
	"github.com/ik-vidya/sim800ctl/pkg/pdu"
)

// Глобальные флаги.
var (
	flagPort        string
	flagBaud        int
	flagPIN         string
	flagCountryCode string
	flagLocalLength int
	flagVerbose     bool
	flagTimeout     time.Duration
)

var (
	version   = "dev"
	buildTime = "unknown"
)

func SetVersion(v, t string) { version, buildTime = v, t }

func Execute() error {
	return rootCmd.Execute()
}

var rootCmd = &cobra.Command{
	Use:   "sim800ctl",
	Short: "Отправка и приём SMS через SIM800L и совместимые модемы",
	Long: `sim800ctl — кроссплатформенная утилита работы с SMS через
GSM-модемы на базе SIM800L (SIM800, SIM900, SIM7000, SIM7600 и др.).

Позволяет отправлять и принимать SMS, диагностировать модем,
разблокировать SIM-карту по PIN и работать в интерактивном режиме.`,
	SilenceUsage:  true,
	SilenceErrors: true,
}

func init() {
	pf := rootCmd.PersistentFlags()
	pf.StringVarP(&flagPort, "port", "p", "", "имя последовательного порта (/dev/ttyUSB0 или COM3)")
	pf.IntVarP(&flagBaud, "baud", "b", 0, "скорость порта (по умолчанию — из настроек или 115200)")
	pf.StringVar(&flagPIN, "pin", "", "PIN SIM-карты (если включён)")
	pf.StringVar(&flagCountryCode, "country-code", "371", "код страны для локальных номеров")
	pf.IntVar(&flagLocalLength, "local-length", 8, "длина локального номера без кода страны (0 — отключить)")
	pf.BoolVarP(&flagVerbose, "verbose", "v", false, "печатать AT-команды и ответы модема")
	pf.DurationVar(&flagTimeout, "timeout", 90*time.Second, "общий таймаут ожидания сети при отправке")

	rootCmd.AddCommand(
		cmdListPorts(),
		cmdDiagnose(),
		cmdRecv(),
		cmdSelfTest(),
		cmdSend(),
		cmdExec(),
		cmdInteractive(),
		cmdVersion(),
	)
}

func resolveBaud(s settings.Settings) int {
	if flagBaud > 0 {
		return flagBaud
	}
	if s.Baud > 0 {
		return s.Baud
	}
	return 115200
}

func openModem() (*modem.Modem, error) {
	if flagPort == "" {
		return nil, fmt.Errorf("не указан порт (-p / --port)")
	}
	s := settings.Load()
	baud := resolveBaud(s)
	logf := func(s string) { fmt.Fprintln(os.Stderr, s) }
	m, err := modem.New(flagPort, baud, flagVerbose, logf)
	if err != nil {
		return nil, err
	}
	if s.Port != flagPort || s.Baud != baud {
		s.Port = flagPort
		s.Baud = baud
		_ = settings.Save(s)
	}
	return m, nil
}

func numberCfg() number.Config {
	return number.Config{
		CountryCode: flagCountryCode,
		LocalLength: flagLocalLength,
	}
}

// ======================== list-ports ========================

func cmdListPorts() *cobra.Command {
	return &cobra.Command{
		Use:   "list-ports",
		Short: "Показать доступные последовательные порты",
		RunE: func(_ *cobra.Command, _ []string) error {
			ports, err := enumerator.GetDetailedPortsList()
			if err != nil {
				return fmt.Errorf("не удалось перечислить порты: %w", err)
			}
			if len(ports) == 0 {
				fmt.Println("Порты не найдены.")
				return nil
			}
			for _, p := range ports {
				desc := p.Product
				if desc == "" {
					desc = "(без описания)"
				}
				fmt.Printf("%-20s %s\n", p.Name, desc)
			}
			return nil
		},
	}
}

// ======================== exec ========================

func cmdExec() *cobra.Command {
	var timeout time.Duration
	cmd := &cobra.Command{
		Use:   "exec <AT-команда>",
		Short: "Выполнить произвольную AT-команду",
		Long: `Выполняет одну AT-команду на модеме и печатает ответ.

Примеры:
    sim800ctl -p /dev/ttyUSB0 exec 'AT+CSCA?'
    sim800ctl -p /dev/ttyUSB0 exec 'AT+COPS?'
    sim800ctl -p /dev/ttyUSB0 exec 'AT+CSQ'

Если команда содержит пробелы или специальные символы — оберните в кавычки.`,
		Args: cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			m, err := openModem()
			if err != nil {
				return err
			}
			defer m.Close()

			resp, err := m.Exec(args[0], timeout)
			if resp != "" {
				fmt.Fprintln(os.Stderr, resp)
			}
			if err != nil {
				return err
			}
			return nil
		},
	}
	cmd.Flags().DurationVar(&timeout, "exec-timeout", 10*time.Second,
		"таймаут ожидания ответа")
	return cmd
}

// ======================== diagnose ========================

func cmdDiagnose() *cobra.Command {
	var (
		t1, t2, t3, t4, t5, t6, t7, t8 bool
		allSafe                        bool
		reportPath                     string
		comparePath                    string
	)

	cmd := &cobra.Command{
		Use:   "diagnose",
		Short: "Диагностика модема: осмотр → измерение → восстановление → проверка",
		Long:  diagnoseHelp,
		RunE: func(_ *cobra.Command, _ []string) error {
			if allSafe {
				t1, t2, t3 = true, true, true
			}
			m, err := openModem()
			if err != nil {
				return err
			}
			defer m.Close()

			selected := map[string]bool{
				"test1": t1, "test2": t2, "test3": t3, "test4": t4,
				"test5": t5, "test6": t6, "test7": t7, "test8": t8,
			}
			var toRun []string
			for _, name := range modem.KnownTests() {
				if selected[name] {
					toRun = append(toRun, name)
				}
			}
			if len(toRun) == 0 {
				toRun = []string{"test1", "test2", "test3"}
			}

			results := make([]modem.TestResult, 0, len(toRun))
			for _, name := range toRun {
				res, err := m.RunTest(name)
				if err != nil {
					return err
				}
				printTestResult(res)
				results = append(results, res)
			}
			recommendNext(results)

			if reportPath != "" {
				rep := modem.NewReport(flagPort, version, results)
				if err := rep.Save(reportPath); err != nil {
					fmt.Fprintf(os.Stderr,
						"\nпредупреждение: не удалось сохранить отчёт: %v\n", err)
				} else {
					fmt.Fprintf(os.Stderr, "\nОтчёт сохранён: %s\n", reportPath)
				}
			}
			if comparePath != "" {
				oldRep, err := modem.LoadReport(comparePath)
				if err != nil {
					fmt.Fprintf(os.Stderr,
						"\nпредупреждение: не удалось загрузить отчёт: %v\n", err)
				} else {
					newRep := modem.NewReport(flagPort, version, results)
					changes := modem.CompareReports(oldRep, newRep)
					fmt.Fprintln(os.Stderr, "")
					fmt.Fprintln(os.Stderr, "=== СРАВНЕНИЕ С ПРЕДЫДУЩИМ ОТЧЁТОМ ===")
					if len(changes) == 0 {
						fmt.Fprintln(os.Stderr, "Изменений нет.")
					} else {
						for _, c := range changes {
							fmt.Fprintf(os.Stderr, "  %s\n", c)
						}
					}
				}
			}
			return nil
		},
	}

	f := cmd.Flags()
	f.BoolVar(&t1, "test1", false, "ОСМОТР: идентификация модема")
	f.BoolVar(&t2, "test2", false, "ОСМОТР: функциональный режим (CFUN, CPAS, CSQ)")
	f.BoolVar(&t3, "test3", false, "ОСМОТР: состояние SIM-карты")
	f.BoolVar(&t4, "test4", false, "ИЗМЕРЕНИЕ: стабильность питания")
	f.BoolVar(&t5, "test5", false, "ИЗМЕРЕНИЕ: сканирование сетей (AT+COPS=?)")
	f.BoolVar(&t6, "test6", false, "ВОССТАНОВЛЕНИЕ: мягкий сброс RF (CFUN=1,1)")
	f.BoolVar(&t7, "test7", false, "ВОССТАНОВЛЕНИЕ: полный сброс (CFUN=0 → CFUN=1,1)")
	f.BoolVar(&t8, "test8", false, "ПРОВЕРКА: мониторинг регистрации 2 минуты")
	f.BoolVar(&allSafe, "all-safe", false, "запустить безопасную группу (test1+test2+test3)")
	f.StringVar(&reportPath, "report", "", "сохранить результаты в JSON-отчёт")
	f.StringVar(&comparePath, "compare", "", "сравнить с прошлым JSON-отчётом")
	return cmd
}

func printTestResult(r modem.TestResult) {
	const line = "========================================================"
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintln(os.Stderr, line)
	fmt.Fprintf(os.Stderr, "Тест: %s\n", r.Name)
	if r.Passed {
		fmt.Fprintln(os.Stderr, "Результат: УСПЕХ")
	} else {
		fmt.Fprintln(os.Stderr, "Результат: ОБНАРУЖЕНА ПРОБЛЕМА")
	}
	if len(r.Findings) > 0 {
		fmt.Fprintln(os.Stderr, "Наблюдения:")
		for _, f := range r.Findings {
			fmt.Fprintf(os.Stderr, "  - %s\n", f)
		}
	}
	if r.Hint != "" {
		fmt.Fprintln(os.Stderr, "Рекомендация:")
		fmt.Fprintf(os.Stderr, "  %s\n", r.Hint)
	}
	fmt.Fprintln(os.Stderr, line)
}

func recommendNext(results []modem.TestResult) {
	hasZeroCSQ, hasCFUN0, hasEmptyScan, hasLowPower := false, false, false, false
	for _, r := range results {
		for _, f := range r.Findings {
			switch {
			case strings.Contains(f, "rssi=0/31"):
				hasZeroCSQ = true
			case strings.Contains(f, "minimum functionality"):
				hasCFUN0 = true
			case strings.Contains(f, "ПУСТОЙ СПИСОК ОПЕРАТОРОВ"):
				hasEmptyScan = true
			case strings.Contains(f, "ПРОСАДКА НИЖЕ 3.6 В"):
				hasLowPower = true
			}
		}
	}
	fmt.Fprintln(os.Stderr, "")
	switch {
	case hasLowPower:
		fmt.Fprintln(os.Stderr, "Следующий шаг: сначала устраните проблему питания.")
	case hasCFUN0:
		fmt.Fprintln(os.Stderr, "Следующий шаг: модем в CFUN=0 — запустите -test6.")
	case hasEmptyScan:
		fmt.Fprintln(os.Stderr, "Следующий шаг: RF-тракт не работает. -test6, затем -test7.")
	case hasZeroCSQ:
		fmt.Fprintln(os.Stderr, "Следующий шаг: сигнал отсутствует. Запустите -test4 и -test5.")
	default:
		fmt.Fprintln(os.Stderr, "Следующий шаг: базовые параметры в норме.")
	}
}

// ======================== recv ========================

func cmdRecv() *cobra.Command {
	var (
		onlyUnread  bool
		fromFilter  string
		limit       int
		deleteAfter bool
		asJSON      bool
		textMode    bool
	)

	cmd := &cobra.Command{
		Use:   "recv",
		Short: "Прочитать входящие SMS из памяти модема",
		Long: `Читает SMS из памяти модема.

По умолчанию используется PDU-режим (AT+CMGF=0): корректно декодирует
и GSM-7 (латиница), и UCS-2 (кириллица). Флаг --text-mode переключает
на устаревший текстовый режим (может искажать GSM-7 на SIM800L).

Флаги:
  --unread           только непрочитанные
  --from <подстрока> фильтр по отправителю
  --limit N          не больше N сообщений
  --delete           удалить прочитанные после вывода
  --json             вывести в формате JSON
  --text-mode        использовать text mode (не рекомендуется)`,
		RunE: func(_ *cobra.Command, _ []string) error {
			m, err := openModem()
			if err != nil {
				return err
			}
			defer m.Close()
			if err := m.Prepare(flagPIN); err != nil {
				return err
			}

			var msgs []modem.SMSMessage
			switch {
			case textMode && onlyUnread:
				msgs, err = m.ReadUnreadSMS()
			case textMode:
				msgs, err = m.ReadAllSMS()
			case onlyUnread:
				msgs, err = m.ReadUnreadPDU()
			default:
				msgs, err = m.ReadAllPDU()
			}
			if err != nil {
				return err
			}

			if fromFilter != "" {
				filtered := msgs[:0]
				for _, msg := range msgs {
					if strings.Contains(msg.From, fromFilter) {
						filtered = append(filtered, msg)
					}
				}
				msgs = filtered
			}
			if limit > 0 && len(msgs) > limit {
				msgs = msgs[:limit]
			}

			if asJSON {
				if msgs == nil {
					msgs = []modem.SMSMessage{}
				}
				out, _ := json.MarshalIndent(msgs, "", "  ")
				fmt.Println(string(out))
			} else {
				if len(msgs) == 0 {
					fmt.Fprintln(os.Stderr, "Нет сообщений.")
				}
				for _, msg := range msgs {
					fmt.Printf("[%d] %s\n", msg.Index, msg.Stat)
					fmt.Printf("    От:    %s\n", msg.From)
					fmt.Printf("    Дата:  %s\n", msg.SCTS)
					if msg.Encoding != "" {
						fmt.Printf("    Код:   %s\n", msg.Encoding)
					}
					fmt.Printf("    Текст: %s\n", msg.Text)
					fmt.Println()
				}
			}

			if deleteAfter {
				for _, msg := range msgs {
					if err := m.DeleteSMS(msg.Index); err != nil {
						fmt.Fprintf(os.Stderr,
							"предупреждение: не удалось удалить SMS %d: %v\n",
							msg.Index, err)
					}
				}
				fmt.Fprintf(os.Stderr, "Удалено %d сообщений.\n", len(msgs))
			}
			return nil
		},
	}

	f := cmd.Flags()
	f.BoolVar(&onlyUnread, "unread", false, "только непрочитанные SMS")
	f.StringVar(&fromFilter, "from", "", "фильтр по отправителю (подстрока)")
	f.IntVar(&limit, "limit", 0, "не больше N сообщений (0 = все)")
	f.BoolVar(&deleteAfter, "delete", false, "удалить прочитанные после вывода")
	f.BoolVar(&asJSON, "json", false, "вывести в формате JSON")
	f.BoolVar(&textMode, "text-mode", false, "использовать text mode (не рекомендуется)")
	return cmd
}

// ======================== selftest ========================

func cmdSelfTest() *cobra.Command {
	var (
		numberFlag string
		text       string
		wait       time.Duration
		keep       bool
		asJSON     bool
	)

	cmd := &cobra.Command{
		Use:   "selftest",
		Short: "Самопроверка: отправить SMS себе и убедиться в приёме",
		Long: `Полная самопроверка модема: отправка SMS самому себе и
проверка получения через PDU-декодер.

Флаги:
  --number <номер>  номер для самотеста
  --text <текст>    базовый текст
  --wait <duration> сколько ждать возврата SMS (по умолчанию 120s)
  --keep            не удалять пришедшее SMS из памяти
  --json            вывести результат в JSON`,
		RunE: func(_ *cobra.Command, _ []string) error {
			m, err := openModem()
			if err != nil {
				return err
			}
			defer m.Close()
			if err := m.Prepare(flagPIN); err != nil {
				return err
			}
			if err := m.EnsureSignal(); err != nil {
				return err
			}
			if err := m.WaitForNetwork(flagTimeout); err != nil {
				return err
			}

			to := numberFlag
			if to == "" {
				s := settings.Load()
				if len(s.Numbers) > 0 {
					to = s.Numbers[0]
				}
			}
			if to == "" {
				return fmt.Errorf("не указан номер: используйте --number")
			}
			to = number.Normalize(numberCfg(), to)

			if text == "" {
				text = "SELFTEST " + time.Now().Format("15:04:05")
			}
			if !asJSON {
				fmt.Fprintf(os.Stderr, "Самотест: %s → %s\n", to, text)
				fmt.Fprintf(os.Stderr, "Таймаут ожидания: %s\n\n", wait)
			}

			res, err := m.SelfTest(to, text, wait)
			if err != nil {
				return err
			}

			if asJSON {
				out, _ := json.MarshalIndent(res, "", "  ")
				fmt.Println(string(out))
			} else {
				fmt.Fprintln(os.Stderr, "")
				fmt.Fprintln(os.Stderr, "=== РЕЗУЛЬТАТ САМОТЕСТА ===")
				fmt.Fprintf(os.Stderr, "Отправлено: %q\n", res.TestText)
				fmt.Fprintf(os.Stderr, "Reference:  %s\n", res.SentRef)
				if res.Received {
					fmt.Fprintln(os.Stderr, "Получено:   ДА")
					fmt.Fprintf(os.Stderr, "От:         %s\n", res.RecvFrom)
					fmt.Fprintf(os.Stderr, "Текст:      %q\n", res.RecvText)
					fmt.Fprintf(os.Stderr, "Время:      %s\n",
						res.WaitTime.Round(time.Millisecond))
					fmt.Fprintln(os.Stderr, "")
					fmt.Fprintln(os.Stderr, "OK: МОДЕМ ПОЛНОСТЬЮ РАБОТОСПОСОБЕН")
				} else {
					fmt.Fprintln(os.Stderr, "Получено:   НЕТ")
					fmt.Fprintf(os.Stderr, "Ожидание:   %s (истекло)\n",
						res.WaitTime.Round(time.Millisecond))
					return fmt.Errorf("самотест не пройден")
				}
			}

			if !keep && res.Received {
				msgs, _ := m.ReadAllPDU()
				token := extractToken(res.TestText)
				for _, msg := range msgs {
					if strings.Contains(msg.Text, token) {
						_ = m.DeleteSMS(msg.Index)
						break
					}
				}
			}
			return nil
		},
	}

	f := cmd.Flags()
	f.StringVar(&numberFlag, "number", "", "номер для самотеста")
	f.StringVar(&text, "text", "", "базовый текст (по умолчанию 'SELFTEST <время>')")
	f.DurationVar(&wait, "wait", 120*time.Second, "сколько ждать возврата SMS")
	f.BoolVar(&keep, "keep", false, "не удалять пришедшее SMS из памяти")
	f.BoolVar(&asJSON, "json", false, "вывести результат в JSON")
	return cmd
}

func extractToken(text string) string {
	if i := strings.LastIndex(text, "["); i >= 0 {
		if j := strings.LastIndex(text, "]"); j > i {
			return text[i : j+1]
		}
	}
	return text
}

// ======================== send ========================

func cmdSend() *cobra.Command {
	var (
		forceSend bool
		dryRun    bool
		fromFile  string
		waitDR    time.Duration
	)

	cmd := &cobra.Command{
		Use:   "send <номер|--from-file file> <текст>",
		Short: "Отправить SMS",
		Long: `Отправляет SMS на указанный номер или на список номеров из файла.

Флаги:
  --force-send        отправлять даже при отсутствии сигнала
  --dry-run           показать PDU и не отправлять
  --from-file <file>  список номеров (по одному в строке)
  --delivery <dur>    ждать +CDS указанное время (0 = не ждать)`,
		Args: cobra.MinimumNArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			// Разбираем аргументы: либо <номер> <текст>, либо <текст> (с --from-file).
			var numbers []string
			var text string

			if fromFile != "" {
				text = args[0]
				data, err := os.ReadFile(fromFile)
				if err != nil {
					return fmt.Errorf("чтение %s: %w", fromFile, err)
				}
				for _, line := range strings.Split(string(data), "\n") {
					line = strings.TrimSpace(line)
					if line == "" || strings.HasPrefix(line, "#") {
						continue
					}
					numbers = append(numbers, line)
				}
				if len(numbers) == 0 {
					return fmt.Errorf("файл %s не содержит номеров", fromFile)
				}
			} else {
				if len(args) != 2 {
					return fmt.Errorf("нужно: send <номер> <текст>")
				}
				numbers = []string{args[0]}
				text = args[1]
			}

			// Нормализуем номера и валидируем.
			normalized := make([]string, 0, len(numbers))
			for _, n := range numbers {
				norm := number.Normalize(numberCfg(), n)
				if err := number.Validate(norm); err != nil {
					return fmt.Errorf("некорректный номер %q: %w", n, err)
				}
				normalized = append(normalized, norm)
			}

			// Общие опции сборки: UCS-2 (обход бага SIM800L),
			// плюс TP-SRR если запрошен delivery report.
			buildOpts := pdu.BuildOptions{
				ForceUCS2: true,
				RequestDR: waitDR > 0,
			}
			parts, err := pdu.BuildWith(normalized[0], text, buildOpts)
			if err != nil {
				return fmt.Errorf("ошибка кодирования SMS: %w", err)
			}
			fmt.Fprintln(os.Stderr, pdu.DescribeWith(text, buildOpts))
			if len(parts) > 1 {
				fmt.Fprintf(os.Stderr,
					"Сообщение будет отправлено как %d отдельных SMS.\n", len(parts))
			}

			// --dry-run: только показать PDU и выйти.
			if dryRun {
				fmt.Fprintln(os.Stderr, "")
				fmt.Fprintln(os.Stderr, "=== DRY RUN: SMS не отправлено ===")
				for _, n := range normalized {
					fmt.Fprintf(os.Stderr, "Получатель: %s\n", n)
				}
				for i, p := range parts {
					fmt.Fprintf(os.Stderr, "Часть %d/%d: TPDU len=%d\n",
						i+1, len(parts), p.TpduLength)
					fmt.Fprintf(os.Stderr, "  %s\n", p.Hex)
				}
				return nil
			}

			m, err := openModem()
			if err != nil {
				return err
			}
			defer m.Close()
			if err := m.Prepare(flagPIN); err != nil {
				return err
			}
			if !forceSend {
				if err := m.EnsureSignal(); err != nil {
					return err
				}
			}
			if err := m.WaitForNetwork(flagTimeout); err != nil {
				return err
			}
			if err := m.SetPduMode(); err != nil {
				return err
			}

			success, failed := 0, 0
			for _, num := range normalized {
				// Пересобираем PDU для конкретного номера.
				partsN, err := pdu.BuildWith(num, text, buildOpts)
				if err != nil {
					fmt.Fprintf(os.Stderr, "%s: ошибка кодирования: %v\n", num, err)
					failed++
					continue
				}
				fmt.Fprintf(os.Stderr, "→ %s: ", num)
				var lastRef string
				ok := true
				for i, p := range partsN {
					if len(partsN) > 1 {
						fmt.Fprintf(os.Stderr, "[%d/%d] ", i+1, len(partsN))
					}
					resp, err := m.SendPduWithRetry(p.Hex, p.TpduLength, 3, 5*time.Second)
					if err != nil {
						fmt.Fprintf(os.Stderr, "ошибка: %v\n", err)
						ok = false
						break
					}
					lastRef = extractCMGSRef(resp)
				}
				if ok {
					fmt.Fprintf(os.Stderr, "OK (ref=%s)\n", lastRef)
					success++
					// Опционально: ждём delivery report.
					if waitDR > 0 {
						if dr, err := m.WaitForDeliveryReport(lastRef, waitDR); err == nil {
							fmt.Fprintf(os.Stderr, "  DR: %s (%s)\n",
								dr.StatusText, dr.Peer)
						} else {
							fmt.Fprintf(os.Stderr, "  DR: не получен (%v)\n", err)
						}
					}
				} else {
					failed++
				}
			}

			if len(normalized) > 1 {
				fmt.Fprintf(os.Stderr, "\nИтого: %d успешно, %d с ошибкой\n",
					success, failed)
			}
			s := settings.Load()
			for _, n := range normalized {
				s.Remember(n)
			}
			_ = settings.Save(s)

			if failed > 0 {
				return fmt.Errorf("%d из %d отправок не удалось", failed, len(normalized))
			}
			return nil
		},
	}

	f := cmd.Flags()
	f.BoolVar(&forceSend, "force-send", false, "отправлять даже при отсутствии сигнала")
	f.BoolVar(&dryRun, "dry-run", false, "показать PDU и не отправлять")
	f.StringVar(&fromFile, "from-file", "", "файл со списком номеров (по одному в строке)")
	f.DurationVar(&waitDR, "delivery", 0, "ждать delivery report указанное время (0 = не ждать)")
	return cmd
}

func extractCMGSRef(resp string) string {
	for _, line := range strings.Split(resp, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "+CMGS:") {
			return strings.TrimSpace(strings.TrimPrefix(line, "+CMGS:"))
		}
	}
	return ""
}

// ======================== interactive ========================

func cmdInteractive() *cobra.Command {
	return &cobra.Command{
		Use:   "interactive",
		Short: "Интерактивный режим (отправка SMS с вводом вручную)",
		RunE: func(_ *cobra.Command, _ []string) error {
			m, err := openModem()
			if err != nil {
				return err
			}
			defer m.Close()
			if err := m.Prepare(flagPIN); err != nil {
				return err
			}
			m.Diagnose(flagPIN)

			reader := bufio.NewReader(os.Stdin)
			for {
				fmt.Print("\nНомер (пусто — выход): ")
				raw, _ := reader.ReadString('\n')
				raw = strings.TrimSpace(raw)
				if raw == "" {
					return nil
				}
				num := number.Normalize(numberCfg(), raw)
				if err := number.Validate(num); err != nil {
					fmt.Printf("Некорректный номер: %v\n", err)
					continue
				}
				fmt.Print("Текст: ")
				text, _ := reader.ReadString('\n')
				text = strings.TrimSpace(text)
				if text == "" {
					fmt.Println("Пустой текст, отмена.")
					continue
				}
				parts, err := pdu.BuildWith(num, text,
					pdu.BuildOptions{ForceUCS2: true})
				if err != nil {
					fmt.Printf("Ошибка: %v\n", err)
					continue
				}
				fmt.Fprintln(os.Stderr, pdu.DescribeWith(text,
					pdu.BuildOptions{ForceUCS2: true}))

				if err := m.EnsureSignal(); err != nil {
					fmt.Printf("Сигнал: %v\n", err)
					continue
				}
				if err := m.WaitForNetwork(flagTimeout); err != nil {
					fmt.Printf("Сеть: %v\n", err)
					continue
				}
				if err := m.SetPduMode(); err != nil {
					fmt.Printf("CMGF: %v\n", err)
					continue
				}

				ok := true
				for i, p := range parts {
					if len(parts) > 1 {
						fmt.Fprintf(os.Stderr, "Часть %d из %d\n", i+1, len(parts))
					}
					if _, err := m.SendPduWithRetry(p.Hex, p.TpduLength, 3, 5*time.Second); err != nil {
						fmt.Printf("Ошибка: %v\n", err)
						ok = false
						break
					}
				}
				if ok {
					s := settings.Load()
					s.Remember(num)
					_ = settings.Save(s)
					fmt.Fprintln(os.Stderr, "SMS отправлено.")
				}
			}
		},
	}
}

// ======================== version ========================

func cmdVersion() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Показать версию",
		Run: func(_ *cobra.Command, _ []string) {
			fmt.Printf("sim800ctl %s (сборка %s)\n", version, buildTime)
		},
	}
}
