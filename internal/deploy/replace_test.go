package deploy

import (
	"os"
	"path/filepath"
	"testing"
)

func TestReplaceAndRollback(t *testing.T) {
	dir := t.TempDir()
	exe := filepath.Join(dir, "ptc")
	if err := os.WriteFile(exe, []byte("old"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := Replace(exe, []byte("new")); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(exe); string(b) != "new" {
		t.Fatalf("exe=%q", b)
	}
	if b, _ := os.ReadFile(PrevPath(exe)); string(b) != "old" {
		t.Fatalf("prev=%q", b)
	}
	if err := Replace(exe, []byte("newer")); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(PrevPath(exe)); string(b) != "new" {
		t.Fatalf("prev after second replace=%q", b)
	}
	if err := Rollback(exe); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(exe); string(b) != "new" {
		t.Fatalf("after rollback exe=%q", b)
	}
	if b, _ := os.ReadFile(PrevPath(exe)); string(b) != "newer" {
		t.Fatalf("after rollback prev=%q", b)
	}
	ents, _ := os.ReadDir(dir)
	if len(ents) != 2 {
		t.Fatalf("leftover files: %v", ents)
	}
	in := Install{Version: "0.3.0", Binary: exe, Service: ServiceNohup}
	if err := Save(dir, in); err != nil {
		t.Fatal(err)
	}
	if got, ok := Load(dir); !ok || got.Binary != exe {
		t.Fatalf("load %+v %v", got, ok)
	}
	next := filepath.Join(dir, "another-ptc")
	if err := RecordInstall(dir, next, "0.3.1"); err != nil {
		t.Fatal(err)
	}
	if got, _ := Load(dir); got.Binary != next || got.Previous != "0.3.0" || got.Service != ServiceNohup {
		t.Fatalf("record install retained an obsolete binary or lost metadata: %+v", got)
	}
}
