package main

import (
	"os"

	"github.com/adalundhe/sylk/cmd"
)

func main() {
	if err := cmd.Execute(); err != nil {
		os.Exit(1)
	}
}
