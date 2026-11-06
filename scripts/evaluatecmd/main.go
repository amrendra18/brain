package main

import (
	"brain/scripts/evaluaterun"
	"context"
	"os"
)

func main() {
	root, err := os.Getwd()
	if err != nil {
		os.Exit(2)
	}
	os.Exit(evaluaterun.Run(context.Background(), root, os.Args[1:], os.Stdout))
}
