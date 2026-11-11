//go:build !windows

package main

import (
	"os"
	"syscall"
	"time"
)

// restartSelf replaces the process image with exe (same PID, so systemd
// units and nohup pid files stay valid).
func restartSelf(exe string) error {
	return syscall.Exec(exe, append([]string{exe}, os.Args[1:]...), os.Environ())
}

func waitForExit(pid int, max time.Duration) {
	deadline := time.Now().Add(max)
	for time.Now().Before(deadline) {
		if syscall.Kill(pid, 0) != nil {
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
}
