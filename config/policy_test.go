package config

import (
	"os"
	"testing"
)

func TestLoad(t *testing.T) {
	content := `
addr = ":8080"

[upstreams]
pypi = "https://pypi.org"

[rules]
min_age_days = 7
block_prerelease = true

[overrides]
allow = ["trusted-pkg"]
`
	f, err := os.CreateTemp(t.TempDir(), "policy-*.toml")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(content); err != nil {
		t.Fatal(err)
	}
	f.Close()

	cfg, err := Load(f.Name())
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if cfg.Addr != ":8080" {
		t.Errorf("got addr %q, want %q", cfg.Addr, ":8080")
	}
	if cfg.Rules.MinAgeDays != 7 {
		t.Errorf("got min_age_days %d, want 7", cfg.Rules.MinAgeDays)
	}
	if !cfg.Rules.BlockPrerelease {
		t.Error("expected block_prerelease to be true")
	}
	if len(cfg.Overrides.Allow) != 1 || cfg.Overrides.Allow[0] != "trusted-pkg" {
		t.Errorf("unexpected overrides.allow: %v", cfg.Overrides.Allow)
	}
}

func TestLoad_FileNotFound(t *testing.T) {
	_, err := Load("nonexistent.toml")
	if err == nil {
		t.Error("expected error for missing file, got nil")
	}
}
