package main

import (
	"os"

	"apm/internal/cli"
	"apm/internal/logging"
)

func main() {
	entry := cli.New(os.Stdout, os.Stderr, logging.SetupLogging)
	if err := entry.Execute(os.Args[1:]); err != nil {
		os.Exit(1)
	}
}
