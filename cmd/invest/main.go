package main

import (
	"os"

	"investment-analyzer/internal/apperr"
	"investment-analyzer/internal/cli"
)

func main() {
	if err := cli.Execute(os.Stdin, os.Stdout, os.Stderr); err != nil {
		os.Exit(apperr.ExitCode(err))
	}
}
