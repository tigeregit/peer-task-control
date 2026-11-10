// Generate a fake CUDA SDK blob and a scene of many mid-size files.
// Usage: go run ./testdata/cuda-e2e/generate.go
package main

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"flag"
	"fmt"
	"os"
	"path/filepath"
)

func main() {
	root := flag.String("dir", ".", "cuda-e2e directory (writes sdk/ and scene/)")
	sdkSize := flag.Int64("sdk-bytes", 2<<30, "third-party SDK payload size")
	sceneSize := flag.Int64("scene-bytes", 2<<30, "total scene data size")
	fileSize := flag.Int64("file-bytes", 200<<10, "size of each scene file")
	flag.Parse()
	if *fileSize <= 0 || *sceneSize < *fileSize {
		fmt.Fprintln(os.Stderr, "scene-bytes must be >= file-bytes")
		os.Exit(1)
	}
	n := int(*sceneSize / *fileSize)
	sdkDir := filepath.Join(*root, "sdk")
	sceneDir := filepath.Join(*root, "scene")
	if err := os.MkdirAll(filepath.Join(sdkDir, "include"), 0o755); err != nil {
		fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(sdkDir, "lib"), 0o755); err != nil {
		fatal(err)
	}
	if err := os.MkdirAll(sceneDir, 0o755); err != nil {
		fatal(err)
	}

	mustWrite(filepath.Join(sdkDir, "VERSION"), []byte("fake-cuda-sdk 0.1 (e2e fixture)\n"))
	mustWrite(filepath.Join(sdkDir, "include", "fake_cuda.h"), []byte("#pragma once\n// stub header for ptc e2e\n#define FAKE_CUDA_VERSION 1\n"))
	payload := filepath.Join(sdkDir, "lib", "libfake_cudart.bin")
	fmt.Printf("writing %s (%d bytes)\n", payload, *sdkSize)
	sdkSum, err := writePatternFile(payload, *sdkSize, 0xC0DA0001)
	if err != nil {
		fatal(err)
	}

	fmt.Printf("writing %d scene files x %d bytes in %s\n", n, *fileSize, sceneDir)
	var total int64
	first := ""
	last := ""
	for i := 0; i < n; i++ {
		name := fmt.Sprintf("cam_%05d.bin", i)
		path := filepath.Join(sceneDir, name)
		sum, err := writePatternFile(path, *fileSize, uint32(0x5CE11E00+i))
		if err != nil {
			fatal(err)
		}
		total += *fileSize
		if i == 0 {
			first = sum
		}
		if i == n-1 {
			last = sum
		}
		if (i+1)%1024 == 0 || i+1 == n {
			fmt.Printf("  %d/%d\n", i+1, n)
		}
	}

	manifest := fmt.Sprintf("sdk_payload_bytes=%d\nsdk_payload_sha256=%s\nscene_files=%d\nscene_file_bytes=%d\nscene_total_bytes=%d\nscene_first_sha256=%s\nscene_last_sha256=%s\n",
		*sdkSize, sdkSum, n, *fileSize, total, first, last)
	mustWrite(filepath.Join(*root, "MANIFEST.txt"), []byte(manifest))
	fmt.Print(manifest)
}

func writePatternFile(path string, size int64, seed uint32) (string, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	buf := make([]byte, 1<<20)
	var off int64
	for off < size {
		n := int64(len(buf))
		if off+n > size {
			n = size - off
		}
		fillPattern(buf[:n], seed, off)
		if _, err := f.Write(buf[:n]); err != nil {
			return "", err
		}
		h.Write(buf[:n])
		off += n
	}
	return "sha256:" + hex.EncodeToString(h.Sum(nil)), f.Close()
}

func fillPattern(buf []byte, seed uint32, off int64) {
	var block [8]byte
	for i := 0; i < len(buf); i += 8 {
		v := uint64(seed)<<32 | uint64(off+int64(i))
		binary.LittleEndian.PutUint64(block[:], v)
		n := copy(buf[i:], block[:])
		_ = n
	}
}

func mustWrite(path string, b []byte) {
	if err := os.WriteFile(path, b, 0o644); err != nil {
		fatal(err)
	}
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}
