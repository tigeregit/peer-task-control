//go:build linux || darwin

package worktreeguard

import (
	"errors"
	"os"

	"golang.org/x/sys/unix"
)

func lockFile(f *os.File, shared bool) error {
	mode := unix.LOCK_EX | unix.LOCK_NB
	if shared {
		mode = unix.LOCK_SH | unix.LOCK_NB
	}
	if err := unix.Flock(int(f.Fd()), mode); err != nil {
		if errors.Is(err, unix.EWOULDBLOCK) || errors.Is(err, unix.EAGAIN) {
			return ErrBusy
		}
		return err
	}
	return nil
}

func unlockFile(f *os.File) error {
	return unix.Flock(int(f.Fd()), unix.LOCK_UN)
}
