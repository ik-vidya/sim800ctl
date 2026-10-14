# github.com/ik-vidya/sim800ctl

Кроссплатформенная CLI-утилита для работы с SMS через GSM-модемы
на базе **SIM800L** (а также совместимые: SIM800, SIM900, SIM7000,
SIM7600). Работает в Linux, Windows и macOS, не требует CGO.

Аналог по функциональности `gammu-smsd` в части отправки/приёма SMS,
но заточен под CLI-использование, отладку и диагностику конкретного
модема.

## Возможности

- **Отправка SMS** в PDU-режиме с автоматическим выбором кодировки
  (GSM-7 / UCS-2) и разбиением на части.
- **Приём SMS** через PDU-декодер — корректно читает и латиницу,
  и кириллицу, независимо от прошивки модема.
- **Самопроверка** (`selftest`) — отправка SMS самому себе и проверка
  приёма, полный контроль тракта модем -> сеть -> SMSC -> модем.
- **Delivery reports** (`--delivery`) — ожидание URC `+CDS` от сети.
- **Диагностика** — 8 тестов на 4 группы (осмотр, измерение,
  восстановление, проверка), сравнение отчётов в JSON.
- **Батч-отправка** (`--from-file`) — один текст, много получателей.
- **Прямой доступ** к AT-командам (`exec`).
- **JSON-вывод** для всех основных команд (`--json`).
- **Автоматическая проверка PIN** при старте, не дожидаясь падения
  в середине операции.

## Требования

- Go 1.22+ для сборки из исходников.
- SIM800L или совместимый AT-совместимый модем.

## Сборка

    git clone https://github.com/ik-vidya/github.com/ik-vidya/sim800ctl.git
    cd github.com/ik-vidya/sim800ctl
    make build
    # Или кросс-сборка:
    make build-linux build-windows build-macos

Готовый бинарник — в `bin/`.

## Быстрый старт

    # Linux: дать пользователю доступ к порту
    sudo usermod -aG dialout $USER
    # перелогиниться

    # Список портов
    ./bin/github.com/ik-vidya/sim800ctl list-ports

    # Диагностика без изменений (безопасно)
    ./bin/github.com/ik-vidya/sim800ctl -p /dev/ttyUSB0 diagnose

    # Самотест: отправить SMS себе и проверить приём
    ./bin/github.com/ik-vidya/sim800ctl -p /dev/ttyUSB0 selftest --number +37121234567

    # Отправить SMS с ожиданием delivery report
    ./bin/github.com/ik-vidya/sim800ctl -p /dev/ttyUSB0 send --delivery 90s +37121234567 "Привет"

    # Прочитать входящие
    ./bin/github.com/ik-vidya/sim800ctl -p /dev/ttyUSB0 recv

    # Прямой AT-запрос
    ./bin/github.com/ik-vidya/sim800ctl -p /dev/ttyUSB0 exec 'AT+CSQ'

## Команды

| Команда | Назначение |
|---------|-----------|
| `list-ports` | Список доступных последовательных портов |
| `diagnose` | Диагностика модема (8 тестов по группам) |
| `recv` | Чтение входящих SMS |
| `selftest` | Самопроверка: отправка себе и приём обратно |
| `send` | Отправка SMS (одиночная или батч) |
| `exec '<AT>'` | Произвольная AT-команда |
| `interactive` | Интерактивный режим отправки |
| `version` | Версия утилиты |

## Глобальные флаги

| Флаг | По умолчанию | Описание |
|------|--------------|----------|
| `-p, --port` | — | Имя порта (`/dev/ttyUSB0` или `COM3`) |
| `-b, --baud` | `115200` | Скорость |
| `--pin` | — | PIN SIM-карты |
| `--country-code` | `371` | Код страны для локальных номеров |
| `--local-length` | `8` | Длина локального номера |
| `-v, --verbose` | `false` | Печатать AT-обмен |
| `--timeout` | `90s` | Таймаут ожидания сети при отправке |

## Команда `send` подробнее

    github.com/ik-vidya/sim800ctl -p PORT send [флаги] <номер> <текст>
    github.com/ik-vidya/sim800ctl -p PORT send --from-file numbers.txt <текст>

| Флаг | Описание |
|------|----------|
| `--dry-run` | Показать PDU и не отправлять |
| `--force-send` | Отправить без проверки сигнала |
| `--delivery <dur>` | Ждать delivery report указанное время |
| `--from-file <file>` | Список номеров (по одному в строке) |

Примеры:

    # Сухая проверка PDU
    github.com/ik-vidya/sim800ctl -p /dev/ttyUSB0 send --dry-run +37121234567 "Test"

    # С ожиданием DR 90 секунд
    github.com/ik-vidya/sim800ctl -p /dev/ttyUSB0 send --delivery 90s +37121234567 "Привет"

    # Батч-отправка
    echo "+37121234567" > nums.txt
    echo "+37121234568" >> nums.txt
    github.com/ik-vidya/sim800ctl -p /dev/ttyUSB0 send --from-file nums.txt "Рассылка"

## Команда `recv` подробнее

| Флаг | Описание |
|------|----------|
| `--unread` | Только непрочитанные |
| `--from <строка>` | Фильтр по отправителю |
| `--limit N` | Не больше N сообщений |
| `--delete` | Удалить прочитанные после вывода |
| `--json` | Вывод в JSON |
| `--text-mode` | Устаревший text mode (не рекомендуется) |

Примеры:

    # Прочитать все
    github.com/ik-vidya/sim800ctl -p /dev/ttyUSB0 recv

    # Только непрочитанные, JSON
    github.com/ik-vidya/sim800ctl -p /dev/ttyUSB0 recv --unread --json

    # Прочитать и удалить
    github.com/ik-vidya/sim800ctl -p /dev/ttyUSB0 recv --delete

## Команда `diagnose` подробнее

Тесты разделены на 4 группы:

| Группа | Тесты | Назначение |
|--------|-------|-----------|
| **Осмотр** | `-test1` `-test2` `-test3` | Read-only. Идентификация, CFUN/CPAS/CSQ, статус SIM |
| **Измерение** | `-test4` `-test5` | Одно изменение состояния. Питание, скан сетей |
| **Восстановление** | `-test6` `-test7` | Мягкий и полный сброс RF |
| **Проверка** | `-test8` | Мониторинг 2 минуты |

Без флагов — безопасная группа "осмотр" + рекомендация следующего шага.

    # Все безопасные тесты одной командой
    github.com/ik-vidya/sim800ctl -p /dev/ttyUSB0 diagnose --all-safe

    # Сохранить и сравнить отчёты
    github.com/ik-vidya/sim800ctl -p /dev/ttyUSB0 diagnose --all-safe --report today.json
    github.com/ik-vidya/sim800ctl -p /dev/ttyUSB0 diagnose --all-safe --compare today.json

Полная справка: `github.com/ik-vidya/sim800ctl diagnose --help`.

## Настройки

Утилита сохраняет порт, скорость и историю номеров (до 5):

- Linux: `~/.config/github.com/ik-vidya/sim800ctl/settings.json`
- Windows: `%APPDATA%\github.com/ik-vidya/sim800ctl\settings.json`
- macOS: `~/Library/Application Support/github.com/ik-vidya/sim800ctl/settings.json`

PIN **никогда** не сохраняется.

## Документация

- [docs/SIM800L-NOTES.md](docs/SIM800L-NOTES.md) — специфика чипа
  SIM800L: нестандартное поведение, баги прошивок, способы обхода.

## Лицензия

MIT — см. [LICENSE](LICENSE).

## Разработка

Проект создан с активным использованием AI-ассистента **DeepSeek**
в режиме диалога: обсуждение архитектуры, разбор логов модема,
поиск и исправление багов. Каждое решение проверено на реальном
железе (SIM800L, прошивка `Revision:1418B04SIM800L24`).

Коммиты содержат trailer `Assisted-by: DeepSeek chat`.

Если нашли проблему — открывайте issue. Pull requests приветствуются,
но прошу следовать [CONTRIBUTING.md](CONTRIBUTING.md).
