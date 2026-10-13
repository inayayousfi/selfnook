package seerr

import (
	"errors"
	"testing"

	"github.com/inayayousfi/selfnook/internal/role"
)

type server struct{ name string }

func (s server) Name() string              { return s.name }
func (server) Endpoint() role.Endpoint     { return role.Endpoint{Host: "media", Port: 1} }
func (server) AdminUser() string           { return "" }
func (server) AdminPassword() string       { return "" }
func (server) APIKey() string              { return "" }
func (server) Libraries() (string, string) { return "Films", "TV" }

// Seerr's guide stops on a media server its pages do not offer, instead of
// printing steps for the wrong software.
func TestGuideRefusesAnUnsupportedMediaServer(t *testing.T) {
	var unsupported *role.Unsupported
	_, err := Guide("https://media.example.com", server{"Plex"}, nil, nil, nil)
	if !errors.As(err, &unsupported) || err.Error() != "Seerr cannot work with Plex." {
		t.Errorf("err = %v", err)
	}
}
