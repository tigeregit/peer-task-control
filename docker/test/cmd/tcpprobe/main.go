package main

import (
	"fmt"
	"net"
	"os"
	"time"
)

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintf(os.Stderr, "usage: tcpprobe host:port\n")
		os.Exit(2)
	}
	d := net.Dialer{Timeout: 2 * time.Second}
	c, err := d.Dial("tcp", os.Args[1])
	if err != nil {
		fmt.Fprintf(os.Stderr, "FAIL %s: %v\n", os.Args[1], err)
		os.Exit(1)
	}
	_ = c.Close()
	fmt.Printf("OK %s\n", os.Args[1])
}
