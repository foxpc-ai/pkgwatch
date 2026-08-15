package config

import (
	"fmt"
	"net/url"
	"strings"

	"github.com/BurntSushi/toml"
)

type Policy struct {
	Addr      string            `toml:"addr"`
	Upstreams map[string]string `toml:"upstreams"`
	Rules     Rules             `toml:"rules"`
	Overrides Overrides         `toml:"overrides"`
}

type Rules struct {
	MinAgeDays      int  `toml:"min_age_days"`
	BlockPrerelease bool `toml:"block_prerelease"`
}

type Overrides struct {
	OverrideCommand string `toml:"override_command"`
}

func (o Overrides) AllowAll(incomingCommand string) bool {
	configured := strings.TrimSpace(o.OverrideCommand)
	incoming := strings.TrimSpace(incomingCommand)
	if configured == "" || incoming == "" {
		return false
	}
	return strings.EqualFold(configured, incoming)
}

func Load(path string) (*Policy, error) {
	var cfg Policy
	_, err := toml.DecodeFile(path, &cfg)
	if err != nil {
		return nil, err
	}
	if err := validate(&cfg); err != nil {
		return nil, err
	}
	return &cfg, nil
}

func validate(cfg *Policy) error {
	if strings.TrimSpace(cfg.Addr) == "" {
		return fmt.Errorf("addr must not be empty")
	}
	if cfg.Rules.MinAgeDays < 0 {
		return fmt.Errorf("rules.min_age_days must be >= 0")
	}
	for name, raw := range cfg.Upstreams {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			continue
		}
		u, err := url.Parse(raw)
		if err != nil || u.Scheme == "" || u.Host == "" {
			return fmt.Errorf("upstreams.%s must be a valid absolute URL", name)
		}
	}
	cfg.Overrides.OverrideCommand = strings.TrimSpace(cfg.Overrides.OverrideCommand)
	return nil
}
