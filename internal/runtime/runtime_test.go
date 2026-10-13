package runtime

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/inayayousfi/selfnook/internal/fake"
	"github.com/inayayousfi/selfnook/internal/shell"
)

// Waiting does not give a user access to Docker, so a refused access fails
// at the first check with Docker's own message.
func TestReadyStopsAtOnceWhenDockerRefusesAccess(t *testing.T) {
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "docker"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
	sh := &fake.Shell{Respond: func([]string) shell.Result {
		return shell.Result{Code: 1, Stderr: "permission denied while trying to connect to the Docker daemon socket\n"}
	}}
	d := Docker.New(Options{Shell: sh, Env: func() map[string]string { return nil }, Sleep: func(time.Duration) {}})
	err := d.Ready()
	if err == nil || !strings.Contains(err.Error(), "permission denied") || len(sh.Commands) != 1 {
		t.Errorf("err = %v after %d commands", err, len(sh.Commands))
	}
}
