package run

import (
	"context"
	"encoding/base64"
	"encoding/binary"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf16"
)

type readyOutput struct {
	mu    sync.Mutex
	text  strings.Builder
	ready chan struct{}
	once  sync.Once
}

func (w *readyOutput) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	_, _ = w.text.Write(p)
	if strings.Contains(w.text.String(), "child-started") {
		w.once.Do(func() { close(w.ready) })
	}
	return len(p), nil
}

func TestCancelStopsCommandChildren(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "child-finished")
	var script string
	if runtime.GOOS == "windows" {
		child := "Start-Sleep -Seconds 2; [IO.File]::WriteAllText('" + strings.ReplaceAll(marker, "'", "''") + "','leaked')"
		code := utf16.Encode([]rune(child))
		b := make([]byte, len(code)*2)
		for i, v := range code {
			binary.LittleEndian.PutUint16(b[i*2:], v)
		}
		script = "$p = Start-Process powershell.exe -ArgumentList '-NoProfile -EncodedCommand " + base64.StdEncoding.EncodeToString(b) + "' -PassThru; Write-Output child-started; Wait-Process -Id $p.Id"
	} else {
		path := "'" + strings.ReplaceAll(marker, "'", "'\\''") + "'"
		script = "sh -c 'sleep 2; printf leaked > \"$1\"' sh " + path + " & echo child-started; wait"
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cmd, err := shellCommand(ctx, "", script)
	if err != nil {
		t.Fatal(err)
	}
	out := &readyOutput{ready: make(chan struct{})}
	cmd.Stdout = out
	done := make(chan error, 1)
	go func() { done <- runProcess(cmd) }()
	select {
	case <-out.ready:
	case err := <-done:
		t.Fatalf("command ended before starting its child: %v", err)
	case <-time.After(10 * time.Second):
		t.Fatal("command did not start its child")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("cancelled command did not stop")
	}
	time.Sleep(3 * time.Second)
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("cancelled child continued executing: %v", err)
	}
}
