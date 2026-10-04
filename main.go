// Command notif is Nexora Notif, the official open addon that tells a
// panel's users about their accounts (nexora-panel docs/phase-g.md, track
// GN): the configuration, the database (SQLite, or PostgreSQL by the
// install's choice), its own admins with a TOTP second factor, the manifest,
// the claim-code registration and the health path, the copy of the panel's
// accounts (internal/users) and the outbox that sends (internal/outbox).
//
//	notif                                    serve (the default)
//	notif admin reset-password -user U -pass P
//	notif backup [-o file]                   a copy of the database (SQLite)
//	notif restore -i file                    put a copy back, Notif stopped
//	notif version
//
// Configuration comes from the environment, the way the install passes it
// (internal/config): NEXORA_OPT_PORT, NEXORA_OPT_DATABASE (sqlite|postgres),
// NEXORA_OPT_DATABASE_DSN, NEXORA_OPT_ADMIN_USERNAME, NEXORA_OPT_ADMIN_PASSWORD,
// NEXORA_CLAIM_CODE, NEXORA_DATA_DIR, NEXORA_MANIFEST_FILE.
package main

import (
	"context"
	_ "embed"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"
	// The zone database inside the binary: a host installed as a service
	// may have none, and the quiet hours and dates are counted in a zone.
	_ "time/tzdata"

	"github.com/nexora-vpn/addon-kit/addon"
	"github.com/nexora-vpn/notif/frontend"
	"github.com/nexora-vpn/notif/internal/admins"
	"github.com/nexora-vpn/notif/internal/api"
	"github.com/nexora-vpn/notif/internal/backup"
	"github.com/nexora-vpn/notif/internal/config"
	"github.com/nexora-vpn/notif/internal/db"
)

// The manifest at the repository's root is the one built in; a release
// signs it before the tag, so the binary serves the signed copy.
//
//go:embed nexora-addon.json
var builtinManifest []byte

func main() {
	log.SetFlags(log.LstdFlags)
	args := os.Args[1:]
	cmd := "run"
	if len(args) > 0 {
		cmd, args = args[0], args[1:]
	}
	var err error
	switch cmd {
	case "run":
		err = run()
	case "admin":
		err = adminCmd(args)
	case "backup":
		err = backupCmd(args)
	case "restore":
		err = restoreCmd(args)
	case "version", "-v", "--version":
		fmt.Println("notif", version())
	default:
		err = fmt.Errorf("unknown command %q: run, admin reset-password, backup, restore, version", cmd)
	}
	if err != nil {
		log.Fatal(err)
	}
}

func version() string {
	a, err := addon.New(addon.Config{Manifest: builtinManifest})
	if err != nil {
		return "unknown"
	}
	return a.Manifest().Version
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	gdb, err := db.Open(cfg)
	if err != nil {
		return err
	}
	if made, err := admins.EnsureFirst(gdb, cfg.AdminUsername, cfg.AdminPassword); err != nil {
		return fmt.Errorf("the first admin: %w", err)
	} else if made {
		log.Printf("made the first admin, %q, from the install's answers", cfg.AdminUsername)
	}
	raw := builtinManifest
	if cfg.ManifestFile != "" {
		if raw, err = os.ReadFile(cfg.ManifestFile); err != nil {
			return fmt.Errorf("NEXORA_MANIFEST_FILE: %w", err)
		}
	}
	a, err := addon.New(addon.Config{Manifest: raw, DataDir: cfg.DataDir, Healthy: api.Healthy(gdb)}.FromEnv())
	if err != nil {
		return err
	}
	var spa fs.FS
	if sub, err := fs.Sub(frontend.Dist, "dist"); err == nil {
		if _, err := fs.Stat(sub, "index.html"); err == nil {
			spa = sub
		}
	}
	app := api.New(gdb, a, cfg, a.Manifest().Version, spa)
	bg, stopBg := context.WithCancel(context.Background())
	defer stopBg()
	go app.Run(bg)
	go backup.Run(bg, gdb, cfg)
	srv := &http.Server{Addr: ":" + cfg.Port, Handler: app.Handler(), ReadHeaderTimeout: 10 * time.Second}
	log.Printf("nexora notif %s on :%s (%s)", a.Manifest().Version, cfg.Port, cfg.Driver)
	errs := make(chan error, 1)
	go func() { errs <- srv.ListenAndServe() }()
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	select {
	case err := <-errs:
		if !errors.Is(err, http.ErrServerClosed) {
			return err
		}
	case <-stop:
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		return srv.Shutdown(ctx)
	}
	return nil
}

// adminCmd is the recovery path: set an admin's password (making the
// account if it is not there), turning its second factor off and ending its
// sessions.
func adminCmd(args []string) error {
	if len(args) == 0 || args[0] != "reset-password" {
		return errors.New("usage: notif admin reset-password -user U -pass P")
	}
	fl := flag.NewFlagSet("reset-password", flag.ContinueOnError)
	user := fl.String("user", "admin", "the admin's username")
	pass := fl.String("pass", "", "the new password (at least 10 characters)")
	if err := fl.Parse(args[1:]); err != nil {
		return err
	}
	if *pass == "" {
		return errors.New("-pass is required")
	}
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	gdb, err := db.Open(cfg)
	if err != nil {
		return err
	}
	if err := admins.ResetPassword(gdb, *user, *pass); err != nil {
		return err
	}
	fmt.Printf("the password of %q is set; its two-factor sign-in is off and its sessions ended\n", *user)
	return nil
}

// backupCmd writes a copy of the database now.
func backupCmd(args []string) error {
	fl := flag.NewFlagSet("backup", flag.ContinueOnError)
	out := fl.String("o", "", "where to write the copy (default <data>/backups/notif-<time>.db)")
	if err := fl.Parse(args); err != nil {
		return err
	}
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	gdb, err := db.Open(cfg)
	if err != nil {
		return err
	}
	path := *out
	if path == "" {
		path = filepath.Join(backup.Dir(cfg), "notif-"+time.Now().UTC().Format("20060102-150405")+".db")
	}
	if err := backup.Snapshot(gdb, cfg, path); err != nil {
		return err
	}
	fmt.Println("written", path)
	return nil
}

// restoreCmd puts a copy back; Notif must be stopped.
func restoreCmd(args []string) error {
	fl := flag.NewFlagSet("restore", flag.ContinueOnError)
	in := fl.String("i", "", "the copy to put back")
	if err := fl.Parse(args); err != nil {
		return err
	}
	if *in == "" {
		return errors.New("-i is required")
	}
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	if err := backup.Restore(cfg, *in); err != nil {
		return err
	}
	fmt.Println("restored; the database it replaced is", cfg.DSN+".before-restore — start Notif again")
	return nil
}
