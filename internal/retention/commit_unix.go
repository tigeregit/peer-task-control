//go:build !windows

package retention

import (
	"errors"
	"os"
	"path/filepath"
)

func commit(src, dst string) error {
	if err := os.Rename(src, dst); err != nil {
		return err
	}
	dir := filepath.Dir(dst)
	for range 3 {
		f, err := os.Open(dir)
		if err != nil {
			return err
		}
		err = errors.Join(f.Sync(), f.Close())
		if err != nil {
			return err
		}
		dir = filepath.Dir(dir)
	}
	return nil
}
