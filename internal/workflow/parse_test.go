package workflow

import (
	"path/filepath"
	"runtime"
	"testing"
)

func testdata() string {
	_, file, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(file), "..", "..", "testdata", "submit")
}

func TestLoadJob(t *testing.T) {
	loaded, err := LoadJob(filepath.Join(testdata(), "workflow.yaml"), "")
	if err != nil {
		t.Fatal(err)
	}
	if loaded.JobName != "test" || len(loaded.RunSteps) != 1 {
		t.Fatalf("job=%s runs=%d", loaded.JobName, len(loaded.RunSteps))
	}
	if loaded.Prepare.Kind != "Prepare" || len(loaded.Prepare.Seeds) != 2 {
		t.Fatalf("prepare=%+v", loaded.Prepare)
	}
	if loaded.Package.Kind != "PackageResult" {
		t.Fatalf("package kind=%s", loaded.Package.Kind)
	}
}

func TestValidateJobRejectsMissingPackage(t *testing.T) {
	job := Job{Steps: []Step{{
		Name: "Prepare",
		Uses: "ptc/prepare@v0",
		With: map[string]any{"file": "prepare.yaml"},
	}}}
	if err := ValidateJob("test", job); err == nil {
		t.Fatal("expected error")
	}
}
