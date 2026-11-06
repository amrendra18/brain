package main

import (
	"brain/scripts/verifyrun"
	"os"
)

func main() {
	root, err := os.Getwd()
	if err != nil {
		os.Exit(3)
	}
	os.Exit(verifyrun.Run(root, os.Args[1:], os.Stdout))
}
