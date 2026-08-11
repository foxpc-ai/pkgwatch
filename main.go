package main

import (
	"flag"
	"log"
	"net/http"

	"github.com/foxpc-ai/pkgwatch/config"
	"github.com/foxpc-ai/pkgwatch/proxy"
)

func main() {
	policyPath := flag.String("policy", "policy.toml", "path to policy.toml")
	flag.Parse()

	cfg, err := config.Load(*policyPath)
	if err != nil {
		log.Fatalf("failed to load policy: %v", err)
	}

	handler := proxy.NewHandler(cfg)

	log.Printf("pkgwatch listening on %s", cfg.Addr)
	log.Printf("  PyPI upstream: %s", cfg.Upstreams["pypi"])
	log.Printf("  min_age_days:  %d", cfg.Rules.MinAgeDays)

	if err := http.ListenAndServe(cfg.Addr, handler); err != nil {
		log.Fatal(err)
	}
}
