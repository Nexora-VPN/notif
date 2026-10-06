// Package config reads Notif's configuration from the environment, the way
// the install passes it: the answers to the manifest's options as
// NEXORA_OPT_<KEY>, beside NEXORA_DATA_DIR and NEXORA_MANIFEST_FILE.
package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/nexora-vpn/addon-kit/addon"
	"github.com/nexora-vpn/addon-kit/web"
)

// The database drivers the manifest's "database" option offers.
const (
	DriverSQLite   = "sqlite"
	DriverPostgres = "postgres"
)

// Config is what Notif starts with.
type Config struct {
	Port    string
	DataDir string
	// BasePath is the path everything is served under — the admin web, its
	// API, the panel's calls ("/k3x9…", or "" for the root): the install's
	// `base_path`. Notif has no page for anyone but its admins, so nothing
	// answers outside it.
	BasePath string
	// PublicURL is the address the admins open Notif at (scheme, host,
	// port; the base path is added to it): the install's `public_url`,
	// carried into the settings when it changes (internal/api, setup.go).
	PublicURL string
	// HTTPS is how Notif serves that address itself (web.HTTPSModes): off;
	// panel — the certificate the panel holds for Notif, fetched from it;
	// acme — a certificate from an ACME CA for its domain, acme-http
	// answering the CA on port 80 (HTTPListen) as well; or self-signed, for
	// an address by IP. With it on, Notif's one port (Port) serves HTTPS and
	// nothing plain (docs/phase-h.md P9 in the panel's repository).
	// HTTPSListen is set only by an install from before that, which keeps
	// HTTPS on a listener of its own beside the plain one. ACMEDirectory and
	// ACMEInsecure point a walk at a test CA.
	HTTPS         string
	HTTPSListen   string
	HTTPListen    string
	ACMEDirectory string
	ACMEInsecure  bool
	// Driver is sqlite or postgres. For SQLite, DSN is the file in DataDir;
	// for PostgreSQL it is the install's connection string.
	Driver string
	DSN    string
	// AdminUsername and AdminPassword make the first admin when there is
	// none, and are read for nothing else: a password changed on the admin
	// web is not undone by the .env at the next start.
	AdminUsername string
	AdminPassword string
	// ManifestFile is served instead of the built-in manifest when set (a
	// development-signed copy, for a walk).
	ManifestFile string
	// LocalProxies are the proxies a bot channel may name wherever they are
	// (channel.SetLocalProxies): the operator's to allow, never the admin
	// web's.
	LocalProxies string
}

// Load reads the environment.
func Load() (Config, error) {
	c := Config{
		Port:          option("port", "8097"),
		BasePath:      addon.Option("base_path"),
		PublicURL:     strings.TrimRight(strings.TrimSpace(addon.Option("public_url")), "/"),
		HTTPS:         strings.ToLower(option("https", web.HTTPSOff)),
		HTTPSListen:   os.Getenv("NEXORA_HTTPS_LISTEN"),
		HTTPListen:    env("NEXORA_HTTP_LISTEN", ":80"),
		ACMEDirectory: os.Getenv("NEXORA_ACME_DIRECTORY"),
		ACMEInsecure:  os.Getenv("NEXORA_ACME_INSECURE") == "1",
		DataDir:       env("NEXORA_DATA_DIR", "data"),
		Driver:        strings.ToLower(option("database", DriverSQLite)),
		DSN:           strings.TrimSpace(addon.Option("database_dsn")),
		AdminUsername: option("admin_username", "admin"),
		AdminPassword: addon.Option("admin_password"),
		ManifestFile:  os.Getenv("NEXORA_MANIFEST_FILE"),
		LocalProxies:  strings.TrimSpace(addon.Option("local_proxies")),
	}
	if !slices.Contains(web.HTTPSModes, c.HTTPS) {
		return c, fmt.Errorf("https must be one of %s", strings.Join(web.HTTPSModes, ", "))
	}
	base, err := web.BasePath(c.BasePath)
	if err != nil {
		return c, fmt.Errorf("base_path: %w", err)
	}
	c.BasePath = base
	switch c.Driver {
	case DriverSQLite:
		c.DSN = filepath.Join(c.DataDir, "notif.db")
	case DriverPostgres:
		if c.DSN == "" {
			return c, errors.New("database is postgres but database_dsn (NEXORA_OPT_DATABASE_DSN) is empty")
		}
	default:
		return c, errors.New("database must be sqlite or postgres")
	}
	return c, nil
}

func option(key, def string) string {
	if v := strings.TrimSpace(addon.Option(key)); v != "" {
		return v
	}
	return def
}

func env(key, def string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return def
}

// OnePort is whether Notif's port serves HTTPS itself, with no plain HTTP
// beside it: HTTPS on, and no listener of its own from an older install.
func (c Config) OnePort() bool { return c.HTTPS != "off" && c.HTTPSListen == "" }
