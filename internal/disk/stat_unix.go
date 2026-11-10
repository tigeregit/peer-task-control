//go:build unix

package disk

import (
	"os"
	"path/filepath"
	"strconv"
	"syscall"
)

func volumeOf(path string) (total, free int64, key string, ok bool) {
	p := path
	for {
		if st, err := os.Stat(p); err == nil && st.IsDir() {
			break
		}
		parent := filepath.Dir(p)
		if parent == p {
			break
		}
		p = parent
	}
	if err := os.MkdirAll(p, 0o755); err != nil {
		p = filepath.Dir(p)
	}
	var st syscall.Statfs_t
	if err := syscall.Statfs(p, &st); err != nil {
		return 0, 0, "", false
	}
	bs := int64(st.Bsize)
	if bs <= 0 {
		return 0, 0, "", false
	}
	total = bs * int64(st.Blocks)
	free = bs * int64(st.Bavail)
	var stt syscall.Stat_t
	if err := syscall.Stat(p, &stt); err == nil {
		key = "dev:" + strconv.FormatUint(uint64(stt.Dev), 16)
	} else {
		key = "fs:" + strconv.FormatUint(uint64(st.Blocks), 10)
	}
	return total, free, key, true
}
