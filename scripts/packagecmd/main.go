package main

import (
	"brain/scripts/release"
	"os"
)

func main() {
	root, err := os.Getwd()
	if err != nil {
		os.Exit(1)
	}
	os.Exit(release.Run(root, os.Args[1:], os.Stdout, os.Stderr))
}
