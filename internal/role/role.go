// Package role defines what apps offer each other. An app never imports
// another app: it declares the roles it needs and reads them from a Set,
// so the app that fills a role can be replaced without changing its users.
package role

import (
	"fmt"

	"github.com/inayayousfi/selfnook/internal/runtime"
)

// Every role gives Name: the software that fills it as people know it,
// such as Sonarr. An app that must name other software in its own pages or
// files keeps a list of the names it supports and reports Unsupported for
// any other, instead of assuming which app fills a role.

// Unsupported reports that App cannot work with the software Other.
type Unsupported struct {
	App, Other string
}

func (e *Unsupported) Error() string {
	return e.App + " cannot work with " + e.Other + "."
}

// Access is the single address that serves every web page.
type Access interface {
	Name() string
	// URL is the address, such as https://media.example.com.
	URL() string
	// Host is the name in URL, such as media.example.com.
	Host() string
	// Secure reports whether the pages are served over HTTPS.
	Secure() bool
}

// SignIn is the admin sign-in that protects the administration pages.
type SignIn interface {
	Name() string
	User() string
	Password() string
	// URL is the address of the sign-in service's own pages.
	URL() string
	// Issuer is the OpenID Connect address of the client with the given ID.
	Issuer(client string) string
	// AdminGroup is the group whose members administer the stack; clients
	// read it from the groups claim.
	AdminGroup() string
	// Check is where the proxy named proxy asks whether a request is signed in.
	Check(proxy string) (Check, error)
	// Ready reports whether the sign-in page works, which can take minutes
	// after the first start.
	Ready(r runtime.Runtime) (bool, error)
}

// Check is an address inside the stack network that answers whether a
// request is signed in.
type Check struct {
	Upstream string
	Path     string
}

// Gateway carries the traffic of the services that must not reach the
// internet directly. Those services share the network of Service.
type Gateway interface {
	Service() string
	// Name is the gateway as people know it, such as Gluetun.
	Name() string
	// Ready reports whether traffic can pass through the gateway. It fails
	// with *GatewayDown when the gateway's container does not run, which
	// waiting cannot fix.
	Ready(r runtime.Runtime) (bool, error)
	// Problem is a sentence from the gateway's recent messages that tells
	// why traffic cannot pass yet. It is empty when they tell nothing.
	Problem(r runtime.Runtime) string
}

// GatewayDown reports a gateway whose container does not run. State is the
// container's state, empty when the container does not exist.
type GatewayDown struct {
	Name, Service, State string
}

func (e *GatewayDown) Error() string {
	container := runtime.Container(e.Service)
	if e.State == "" {
		return e.Name + " does not run: its container " + container + " does not exist."
	}
	return e.Name + " does not run: its container " + container + " is " + e.State + "."
}

// Running fails with *GatewayDown unless the container of the gateway named
// name, which runs as service, is running or restarting.
func Running(r runtime.Runtime, name, service string) error {
	state, err := r.State(service)
	if err != nil {
		return err
	}
	if state != "running" && state != "restarting" {
		return &GatewayDown{Name: name, Service: service, State: state}
	}
	return nil
}

// Endpoint is how a container reaches an app inside the stack network.
type Endpoint struct {
	// Service is the Compose service that runs the app.
	Service string
	// Host and Port are the address other containers use.
	Host string
	Port int
}

// DownloadClient downloads what the libraries request.
type DownloadClient interface {
	Name() string
	Endpoint() Endpoint
	// Login is what other containers sign in with. Both are empty when the
	// client lets the stack network in without a login.
	Login() (user, password string)
}

// IndexerManager searches indexers for the libraries.
type IndexerManager interface {
	Name() string
	Endpoint() Endpoint
}

// Library manages one kind of media, such as movies or series.
type Library interface {
	Name() string
	Endpoint() Endpoint
	// WebPath is the path of the app's web pages at the access address.
	WebPath() string
	// RootFolder is where the app keeps its media, inside the containers.
	RootFolder() string
	// MediaServerSteps are the steps, without numbers, that make the app
	// refresh server after each import.
	MediaServerSteps(server MediaServer) ([]string, error)
	// StatusPath answers with the app's status when the API key is sent
	// in the X-Api-Key header.
	StatusPath() string
	// APIKey is the key the app generated, which other apps use to reach it.
	APIKey() string
}

// MediaServer plays the media.
type MediaServer interface {
	Name() string
	Endpoint() Endpoint
	AdminUser() string
	AdminPassword() string
	APIKey() string
	// Libraries are the names of the movie and series libraries its setup creates.
	Libraries() (movies, series string)
}

// QualityProfiles chooses which releases the libraries download.
type QualityProfiles interface {
	Name() string
	// Profile is the quality profile library uses.
	Profile(library Library) (string, error)
}

// Role names one role, for declarations and for the state file.
type Role interface {
	Info() Info
}

// Info describes a role. Title and Prompt are the setup question asked when
// several apps can fill the role.
type Info struct {
	Name   string
	Title  string
	Prompt string
}

// Key identifies a role together with the interface its provider implements.
type Key[T any] struct{ info Info }

func (k Key[T]) Info() Info { return k.info }

// The roles.
var (
	AccessAddress = Key[Access]{Info{Name: "access"}}
	AdminSignIn   = Key[SignIn]{Info{Name: "sign-in"}}
	VPN           = Key[Gateway]{Info{Name: "gateway", Title: "VPN provider", Prompt: "Select a VPN provider"}}
	Downloads     = Key[DownloadClient]{Info{Name: "download-client"}}
	Indexers      = Key[IndexerManager]{Info{Name: "indexer-manager"}}
	Movies        = Key[Library]{Info{Name: "movie-library"}}
	Series        = Key[Library]{Info{Name: "series-library"}}
	Media         = Key[MediaServer]{Info{Name: "media-server"}}
	Profiles      = Key[QualityProfiles]{Info{Name: "quality-profiles"}}
)

// Values is a provider's read access to its own saved values.
type Values interface {
	Get(key string) string
	Has(key string) bool
}

// Offer is one role a provider fills, built from its own values and the
// roles the provider itself needs.
type Offer struct {
	info Info
	make func(Values, Set) any
}

func (o Offer) Info() Info { return o.info }

// Provide offers role k, built by make from the provider's values and roles.
func Provide[T any](k Key[T], make func(v Values, r Set) T) Offer {
	return Offer{info: k.info, make: func(v Values, r Set) any { return make(v, r) }}
}

// Bound is an offer joined to its provider's values and roles.
type Bound struct {
	Offer  Offer
	Values Values
	Roles  Set
}

// Set is what one app may read: the roles it declared, each bound to the
// provider that fills it.
type Set struct {
	owner  string
	needs  []Role
	filled map[string]Bound
}

// NewSet gives owner access to the roles it needs, as filled holds them.
func NewSet(owner string, needs []Role, filled map[string]Bound) Set {
	return Set{owner: owner, needs: needs, filled: filled}
}

// Get returns the provider of role k. Reading a role the app did not
// declare, or one no app fills yet, is a programming error and stops the
// program.
func Get[T any](s Set, k Key[T]) T {
	bound, ok := s.filled[k.info.Name]
	if !ok {
		for _, need := range s.needs {
			if need.Info().Name == k.info.Name {
				panic(fmt.Sprintf("app %s reads role %s before an app fills it", s.owner, k.info.Name))
			}
		}
		panic(fmt.Sprintf("app %s reads role %s, which it does not declare", s.owner, k.info.Name))
	}
	return bound.Offer.make(bound.Values, bound.Roles).(T)
}
