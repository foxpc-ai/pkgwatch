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
override_command = "any_command"
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
	if cfg.Overrides.OverrideCommand != "any_command" {
		t.Errorf("got override_command %q, want %q", cfg.Overrides.OverrideCommand, "any_command")
	}
	if !cfg.Overrides.AllowAll("any_command") {
		t.Error("expected incoming token any_command to match override_command")
	}
	if cfg.Overrides.AllowAll("banana") {
		t.Error("expected non-matching token to keep override disabled")
	}

}

func TestLoad_FileNotFound(t *testing.T) {
	_, err := Load("nonexistent.toml")
	if err == nil {
		t.Error("expected error for missing file, got nil")
	}
}

func TestLoad_RejectsNegativeMinAge(t *testing.T) {
	content := `
addr = ":8080"

[upstreams]
pypi = "https://pypi.org"

[rules]
min_age_days = -1
`
	f, err := os.CreateTemp(t.TempDir(), "policy-*.toml")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(content); err != nil {
		t.Fatal(err)
	}
	_ = f.Close()

	_, err = Load(f.Name())
	if err == nil {
		t.Fatal("expected validation error for negative min_age_days")
	}
}

func TestLoad_RejectsInvalidUpstreamURL(t *testing.T) {
	content := `
addr = ":8080"

[upstreams]
pypi = "not-a-url"
`
	f, err := os.CreateTemp(t.TempDir(), "policy-*.toml")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(content); err != nil {
		t.Fatal(err)
	}
	_ = f.Close()

	_, err = Load(f.Name())
	if err == nil {
		t.Fatal("expected validation error for invalid upstream URL")
	}
}

func TestLoad_RejectsEmptyAddr(t *testing.T) {
	content := `
addr = ""

[upstreams]
pypi = "https://pypi.org"
`
	f, err := os.CreateTemp(t.TempDir(), "policy-*.toml")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(content); err != nil {
		t.Fatal(err)
	}
	_ = f.Close()

	_, err = Load(f.Name())
	if err == nil {
		t.Fatal("expected validation error for empty addr")
	}
}
