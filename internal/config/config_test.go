package config

import "testing"

// TestTheInstallsBasePathAndHTTPS: the base path is read as the kit reads
// it — slashes or none, empty for the root — and a malformed one, or an
// https mode Notif does not have, stops the start.
func TestTheInstallsBasePathAndHTTPS(t *testing.T) {
	for raw, want := range map[string]string{"": "", "/": "", "k9x2": "/k9x2", "/k9x2/": "/k9x2"} {
		t.Setenv("NEXORA_OPT_BASE_PATH", raw)
		c, err := Load()
		if err != nil || c.BasePath != want {
			t.Errorf("%q: %q %v", raw, c.BasePath, err)
		}
	}
	t.Setenv("NEXORA_OPT_BASE_PATH", "a/b")
	if _, err := Load(); err == nil {
		t.Error("a base path of two segments was taken")
	}
	t.Setenv("NEXORA_OPT_BASE_PATH", "")
	for mode, ok := range map[string]bool{"off": true, "acme": true, "self-signed": true, "letsencrypt": false} {
		t.Setenv("NEXORA_OPT_HTTPS", mode)
		if _, err := Load(); (err == nil) != ok {
			t.Errorf("https %s: %v", mode, err)
		}
	}
}
