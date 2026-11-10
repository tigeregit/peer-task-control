//go:build windows

package disk

import (
	"os"
	"path/filepath"
	"syscall"
	"unsafe"
)

var (
	modkernel32            = syscall.NewLazyDLL("kernel32.dll")
	procGetDiskFreeSpaceEx = modkernel32.NewProc("GetDiskFreeSpaceExW")
)

func volumeOf(path string) (total, free int64, key string, ok bool) {
	p := path
	for {
		if st, err := os.Stat(p); err == nil {
			if st.IsDir() {
				break
			}
			p = filepath.Dir(p)
			break
		}
		parent := filepath.Dir(p)
		if parent == p {
			break
		}
		p = parent
	}
	_ = os.MkdirAll(p, 0o755)
	abs, err := filepath.Abs(p)
	if err != nil {
		abs = p
	}
	vol := filepath.VolumeName(abs)
	if vol == "" {
		vol = abs
	}
	pathPtr, err := syscall.UTF16PtrFromString(abs)
	if err != nil {
		return 0, 0, "", false
	}
	var freeBytes, totalBytes, _totalFree int64
	r1, _, _ := procGetDiskFreeSpaceEx.Call(
		uintptr(unsafe.Pointer(pathPtr)),
		uintptr(unsafe.Pointer(&freeBytes)),
		uintptr(unsafe.Pointer(&totalBytes)),
		uintptr(unsafe.Pointer(&_totalFree)),
	)
	if r1 == 0 {
		return 0, 0, "", false
	}
	return totalBytes, freeBytes, vol, true
}
