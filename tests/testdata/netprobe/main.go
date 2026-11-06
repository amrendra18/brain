package main

import (
	"errors"
	"fmt"
	"net"
	"os"
	"syscall"
	"time"
)

func main() {
	connection, err := net.DialTimeout("tcp", os.Args[1], 2*time.Second)
	if os.Args[2] == "blocked" {
		if errors.Is(err, syscall.EPERM) || errors.Is(err, syscall.EACCES) {
			fmt.Println("the OS blocked the connection")
			return
		}
		if connection != nil {
			connection.Close()
		}
		fmt.Fprintln(os.Stderr, "the OS did not block the connection")
		os.Exit(1)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "the control connection did not open")
		os.Exit(1)
	}
	connection.Close()
	fmt.Println("the control connection opened")
}
