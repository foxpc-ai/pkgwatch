package config

import "github.com/BurntSushi/toml"

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
	Allow []string `toml:"allow"`
}

func Load(path string) (*Policy, error) {
	var cfg Policy
	_, err := toml.DecodeFile(path, &cfg)
	return &cfg, err
}
