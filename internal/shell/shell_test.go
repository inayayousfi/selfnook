package shell

import (
	"os"
	"path/filepath"
	"testing"
)

// A Windows search path can hold a stray double quote, which Go reads as
// grouping. Programs in the folders it covers must still be found and run.
func TestQuotedSearchPathEntryStillFindsAndRunsPrograms(t *testing.T) {
	bin := t.TempDir()
	program := filepath.Join(bin, "docker")
	if err := os.WriteFile(program, []byte("#!/bin/sh\necho ran\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", `"`+bin+`"`)

	if path, ok := Found("docker"); !ok || path != program {
		t.Errorf("Found = %q, %v; want %q", path, ok, program)
	}
	result, err := (&OS{Dir: bin}).Run(Cmd{Args: []string{"docker"}, Capture: true, Check: true})
	if err != nil || result.Stdout != "ran\n" {
		t.Errorf("Run = %+v, %v", result, err)
	}
}
