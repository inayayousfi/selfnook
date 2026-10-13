package state

import (
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"testing"
)

// The state file keeps every part through a write and a read, and only its
// owner can read it on Unix, because it holds passwords and API keys.
func TestStateSurvivesAWriteAndStaysPrivate(t *testing.T) {
	path := filepath.Join(t.TempDir(), "selfnook", "state.json")
	saved := New()
	saved.Set(Core, "MEDIA_DIR", "/media path/$disk")
	saved.Set("authentik", "ADMIN_PASS", `p"q\r$`)
	saved.Complete("sonarr")
	saved.Roles["gateway"] = "gluetun"
	saved.Versions["radarr-app"] = Version{Version: "6.3.0.10514", Source: FromDigest, Digest: "sha256:a4"}
	if err := Write(path, saved); err != nil {
		t.Fatal(err)
	}
	read, found, err := Read(path)
	if err != nil || !found || !reflect.DeepEqual(read, saved) {
		t.Errorf("read = %+v, found = %v, err = %v", read, found, err)
	}
	if runtime.GOOS != "windows" {
		if info, _ := os.Stat(path); info.Mode().Perm() != 0o600 {
			t.Errorf("mode = %v", info.Mode().Perm())
		}
	}
}
