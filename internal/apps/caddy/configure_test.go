package caddy

import (
	"testing"

	"github.com/inayayousfi/selfnook/internal/app"
	"github.com/inayayousfi/selfnook/internal/fake"
	"github.com/inayayousfi/selfnook/internal/state"
)

// A saved mode with an unusable address asks again instead of stopping setup.
func TestInvalidSavedAddressIsAskedAgain(t *testing.T) {
	saved := state.New()
	saved.Set("caddy", modeKey, Domain)
	saved.Set("caddy", hostKey, "localhost")
	ui := &fake.UI{Answers: map[string]string{"mode": Domain, "host": "media.example.com"}}
	e := &app.Env{Values: app.ValuesFor(saved, App), UI: ui, Shell: &fake.Shell{}, SaveValues: func() error { return nil }}
	if err := configure(e); err != nil {
		t.Fatal(err)
	}
	if saved.Get("caddy", hostKey) != "media.example.com" {
		t.Errorf("host = %q, events = %v", saved.Get("caddy", hostKey), ui.Events)
	}
}
