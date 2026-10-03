// Package config reads Notif's configuration from the environment, the way
// the install passes it: the answers to the manifest's options as
// NEXORA_OPT_<KEY>, beside NEXORA_DATA_DIR and NEXORA_MANIFEST_FILE.
package config

import (
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/nexora-vpn/addon-kit/addon"
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
}

// Load reads the environment.
func Load() (Config, error) {
	c := Config{
		Port:          option("port", "8097"),
		DataDir:       env("NEXORA_DATA_DIR", "data"),
		Driver:        strings.ToLower(option("database", DriverSQLite)),
		DSN:           strings.TrimSpace(addon.Option("database_dsn")),
		AdminUsername: option("admin_username", "admin"),
		AdminPassword: addon.Option("admin_password"),
		ManifestFile:  os.Getenv("NEXORA_MANIFEST_FILE"),
	}
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
