package main

import (
	"brain/scripts/installrun"
	"os"
)

func main() { os.Exit(installrun.Run(os.Args[1:], os.Stdout)) }
