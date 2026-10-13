package app

import (
	"testing"

	"github.com/inayayousfi/selfnook/internal/state"
)

// An app changes only its own settings and reads only its own and the core
// values; changing another app's value stops the program.
func TestValuesChangeOnlyOwnedSettings(t *testing.T) {
	saved := state.New()
	saved.Set("other", "OTHER", "kept")
	saved.Set(state.Core, "MEDIA_DIR", "/media")
	owner := &App{Name: "demo", Settings: []Setting{{Key: "MINE"}}}
	v := ValuesFor(saved, owner)
	v.Set("MINE", "changed")
	if saved.Get("demo", "MINE") != "changed" || v.Get("MEDIA_DIR") != "/media" || v.Get("OTHER") != "" {
		t.Errorf("MINE = %q, MEDIA_DIR = %q, OTHER = %q", saved.Get("demo", "MINE"), v.Get("MEDIA_DIR"), v.Get("OTHER"))
	}
	defer func() {
		if recover() == nil || saved.Get("other", "OTHER") != "kept" {
			t.Errorf("OTHER = %q after an unowned change", saved.Get("other", "OTHER"))
		}
	}()
	v.Set("OTHER", "taken")
}
