package workflow

import (
	"os"
	"path/filepath"
	"testing"
)

func testdata() string {
	return filepath.Join("..", "..", "testdata", "submit")
}

func TestSeedOnlyWorkflow(t *testing.T) {
	loaded, err := LoadJob(filepath.Join(testdata(), "..", "seed", "workflow.yaml"), "")
	if err != nil {
		t.Fatal(err)
	}
	if !loaded.SeedOnly || loaded.PackagePath != "" || len(loaded.RunSteps) != 0 {
		t.Fatalf("unexpected seed-only job: %+v", loaded)
	}
	for _, step := range []Step{
		{Uses: "ptc/seed@v0", With: map[string]any{"file": "seeds.yaml"}, Run: "echo forbidden"},
		{Uses: "ptc/seed@v0", With: map[string]any{"file": "seeds.yaml"}, If: "false"},
		{Uses: "ptc/seed@v1", With: map[string]any{"file": "seeds.yaml"}},
	} {
		if err := ValidateJob("seed", Job{Steps: []Step{step}}); err == nil {
			t.Fatalf("accepted invalid seed step: %+v", step)
		}
	}
	step := loaded.Job.Steps[0]
	if err := ValidateJob("seed", Job{Steps: []Step{step, {Run: "echo forbidden"}}}); err == nil {
		t.Fatal("seed action accepted additional run step")
	}
}

func TestSeedOnlyRejectsWorkspace(t *testing.T) {
	dir := t.TempDir()
	data := "apiVersion: ptc/v0\nkind: Seed\nworkspace:\n  entrypoint: forbidden\nseeds:\n  - name: data\n    local:\n      src: .\n"
	file := filepath.Join(dir, "seed.yaml")
	if err := os.WriteFile(file, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := finishSeedLoad(Loaded{SeedOnly: true}, file); err == nil {
		t.Fatal("seed action accepted workspace")
	}
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
