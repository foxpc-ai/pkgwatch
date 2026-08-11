package proxy

import (
	"fmt"
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/foxpc-ai/pkgwatch/config"
	"github.com/foxpc-ai/pkgwatch/registries"
)

type responseRecorder struct {
	http.ResponseWriter
	status int
}

func (rec *responseRecorder) WriteHeader(code int) {
	rec.status = code
	rec.ResponseWriter.WriteHeader(code)
}

func NewHandler(cfg *config.Policy) http.Handler {
	mux := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasPrefix(r.URL.Path, "/pypi/"):
			handle(w, r, cfg, registries.PyPI{}, "pypi")
		default:
			http.Error(w, "pkgwatch: no route for path "+r.URL.Path, http.StatusNotFound)
		}
	})

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		recorder := &responseRecorder{ResponseWriter: w, status: http.StatusOK}

		mux.ServeHTTP(recorder, r)

		log.Printf("[HTTP] %s | Status: %d | Duration: %v | Path: %s",
			r.Method, recorder.status, time.Since(start), r.URL.Path)
	})
}

func handle(w http.ResponseWriter, r *http.Request, cfg *config.Policy, reg registries.Registry, ecosystem string) {
	upstream := cfg.Upstreams[ecosystem]
	if upstream == "" {
		http.Error(w, "pkgwatch: no upstream configured for "+ecosystem, http.StatusInternalServerError)
		return
	}

	pkg := reg.ExtractPackage(r)

	if pkg != nil && !isAllowed(cfg, pkg.Name) {
		meta, err := reg.FetchMetadata(r.Context(), pkg, upstream)
		if err != nil {
			http.Error(w, "pkgwatch: metadata error: "+err.Error(), http.StatusBadGateway)
			return
		}

		if violation := checkRules(cfg, meta); violation != "" {
			log.Printf("BLOCKED %s/%s: %s", ecosystem, pkg.Name, violation)
			http.Error(w, "pkgwatch: blocked — "+violation, http.StatusForbidden)
			return
		}

		log.Printf("ALLOWED %s/%s@%s", ecosystem, meta.Name, meta.Version)
	}

	forward(w, r, upstream, "/"+ecosystem)
}

func forward(w http.ResponseWriter, r *http.Request, upstream, stripPrefix string) {
	target, _ := url.Parse(upstream)
	rp := &httputil.ReverseProxy{
		Director: func(req *http.Request) {
			req.URL.Scheme = target.Scheme
			req.URL.Host = target.Host
			req.URL.Path = strings.TrimPrefix(req.URL.Path, stripPrefix)
			req.Host = target.Host
		},
	}
	rp.ServeHTTP(w, r)
}

func checkRules(cfg *config.Policy, meta *registries.PackageMeta) string {
	if cfg.Rules.MinAgeDays > 0 {
		ageDays := int(time.Since(meta.PublishedAt).Hours() / 24)
		if ageDays < cfg.Rules.MinAgeDays {
			return fmt.Sprintf("%s@%s is %d days old (policy requires %d)", meta.Name, meta.Version, ageDays, cfg.Rules.MinAgeDays)
		}
	}
	return ""
}

func isAllowed(cfg *config.Policy, name string) bool {
	return slices.Contains(cfg.Overrides.Allow, name)
}
