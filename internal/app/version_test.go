package app

import (
	"testing"

	"github.com/inayayousfi/selfnook/internal/role"
	"github.com/inayayousfi/selfnook/internal/runtime"
	"github.com/inayayousfi/selfnook/internal/state"
)

// The digest decides first, then the label, then the running app; when none
// tells, the version stays unknown.
func TestDetectionOrder(t *testing.T) {
	image := Image{
		Digests: map[string]string{"sha256:known": "1.0.0"},
		Labels:  map[string]string{"v1.1.0": "1.1.0"},
		Ask:     func(*Env) (string, error) { return "", nil },
	}
	ask := func(version string) func() (string, error) {
		return func() (string, error) { return version, nil }
	}
	cases := []struct {
		found runtime.Image
		ask   string
		want  state.Version
	}{
		{runtime.Image{Digests: []string{"sha256:other", "sha256:known"}, Label: "v1.1.0"}, "9", state.Version{Version: "1.0.0", Source: state.FromDigest, Digest: "sha256:known", Label: "v1.1.0"}},
		{runtime.Image{Digests: []string{"sha256:other"}, Label: "v1.1.0"}, "9", state.Version{Version: "1.1.0", Source: state.FromLabel, Digest: "sha256:other", Label: "v1.1.0"}},
		{runtime.Image{Digests: []string{"sha256:other"}, Label: "v2"}, "2.0.0", state.Version{Version: "2.0.0", Source: state.FromApp, Digest: "sha256:other", Label: "v2"}},
		{runtime.Image{Digests: []string{"sha256:other"}, Label: "v2"}, "", state.Version{Digest: "sha256:other", Label: "v2"}},
	}
	for _, c := range cases {
		if got := Detect(image, c.found, ask(c.ask)); got != c.want {
			t.Errorf("Detect(%+v) = %+v, want %+v", c.found, got, c.want)
		}
	}
}

// An implementation covers the versions it was checked with, and an app
// with no implementation for a version falls back to the pinned one.
func TestImplementationFollowsTheVersion(t *testing.T) {
	a := &App{
		Name:   "demo",
		Images: []Image{{Service: "demo", Digests: map[string]string{"sha256:0123abcd": "1.2.0"}}},
		Implementations: []Implementation{
			{From: "1.0.0", Until: "1.2.0"},
			{From: "2.0.0", Until: "2.0.10", Provides: []role.Offer{role.Provide(role.VPN, func(role.Values, role.Set) role.Gateway { return nil })}},
		},
	}
	a.Compose = fragment("services:\n  demo:\n    image: demo:latest@sha256:0123abcd\n")
	if impl, exact := a.Implementation("2.0.9"); !exact || impl != &a.Implementations[1] {
		t.Error("2.0.9 did not choose the second implementation")
	}
	if impl, exact := a.Implementation("3.0.0"); exact || impl != &a.Implementations[0] {
		t.Error("3.0.0 did not fall back to the pinned implementation")
	}
	if impl, exact := a.Implementation(""); exact || impl != &a.Implementations[0] {
		t.Error("an unknown version did not fall back to the pinned implementation")
	}
	// The roles an app fills follow the same choice.
	if got := a.Provides("2.0.9"); len(got) != 1 || got[0].Name != role.VPN.Info().Name {
		t.Errorf("roles at 2.0.9 = %v", got)
	}
	if got := a.Provides("1.2.0"); len(got) != 0 {
		t.Errorf("roles at 1.2.0 = %v", got)
	}
}
