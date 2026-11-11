//go:build windows

package main

import (
	"os"
	"os/exec"
	"strconv"
	"time"
)

// restartSelf starts exe with the same arguments and exits. Windows has no
// exec(2); the child waits for this PID to exit before it binds the ports.
func restartSelf(exe string) error {
	var args []string
	skip := false
	for _, a := range os.Args[1:] {
		if skip {
			skip = false
			continue
		}
		if a == "--wait-pid" {
			skip = true
			continue
		}
		if len(a) > 11 && a[:11] == "--wait-pid=" {
			continue
		}
		args = append(args, a)
	}
	args = append(args, "--wait-pid", strconv.Itoa(os.Getpid()))
	cmd := exec.Command(exe, args...)
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	if err := cmd.Start(); err != nil {
		return err
	}
	os.Exit(0)
	return nil
}

func waitForExit(pid int, max time.Duration) {
	p, err := os.FindProcess(pid)
	if err != nil {
		return
	}
	done := make(chan struct{})
	go func() { _, _ = p.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(max):
	}
}
