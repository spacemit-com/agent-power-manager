package main

import (
	"os"

	"apm/internal/cli"
	"apm/internal/logging"
)

func main() {
	logger := logging.NewDefault(false)
	if err := cli.New(logger).Execute(os.Args[1:]); err != nil {
		os.Exit(1)
	}
}
