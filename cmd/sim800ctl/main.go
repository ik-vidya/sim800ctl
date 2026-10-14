// Команда sim800ctl — CLI-утилита отправки SMS через SIM800L.
package main

import (
	"fmt"
	"os"

	"github.com/ik-vidya/sim800ctl/internal/cli"
)

// Версия и время сборки подставляются через -ldflags.
var (
	version   = "dev"
	buildTime = "unknown"
)

func main() {
	cli.SetVersion(version, buildTime)
	if err := cli.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "ошибка:", err)
		os.Exit(1)
	}
}