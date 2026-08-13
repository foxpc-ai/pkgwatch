package proxy

import (
	"fmt"
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/foxpc-ai/pkgwatch/config"
	"github.com/foxpc-ai/pkgwatch/registries"
)

type responseRecorder struct {
	http.ResponseWriter
	status int
}

type cacheEntry struct {
	meta      *registries.PackageMeta
	expiresAt time.Time
}

type Handler struct {
	cfg   *config.Policy
	cache sync.Map // map[string]*cacheEntry
}

func (rec *responseRecorder) WriteHeader(code int) {
	rec.status = code
	rec.ResponseWriter.WriteHeader(code)
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	recorder := &responseRecorder{ResponseWriter: w, status: http.StatusOK}

	switch {
	case strings.HasPrefix(r.URL.Path, "/pypi/"):
		h.handle(recorder, r, registries.PyPI{}, "pypi")
	default:
		http.Error(recorder, "pkgwatch: no route for path "+r.URL.Path, http.StatusNotFound)
	}

	log.Printf("[HTTP] %s | Status: %d | Duration: %v | Path: %s",
		r.Method, recorder.status, time.Since(start), r.URL.Path)
}

func NewHandler(cfg *config.Policy) *Handler {
	return &Handler{cfg: cfg}
}

func (h *Handler) handle(w http.ResponseWriter, r *http.Request, reg registries.Registry, ecosystem string) {
	upstream := h.cfg.Upstreams[ecosystem]
	if upstream == "" {
		http.Error(w, "pkgwatch: no upstream configured for "+ecosystem, http.StatusInternalServerError)
		return
	}

	pkg := reg.ExtractPackage(r)

	if pkg != nil && !isAllowed(h.cfg, pkg.Name) {
		meta, err := h.cachedFetch(r, reg, pkg, upstream, ecosystem)
		if err != nil {
			http.Error(w, "pkgwatch: metadata error: "+err.Error(), http.StatusBadGateway)
			return
		}

		if violation := checkRules(h.cfg, meta); violation != "" {
			log.Printf("BLOCKED %s/%s: %s", ecosystem, pkg.Name, violation)
			http.Error(w, "pkgwatch: blocked — "+violation, http.StatusForbidden)
			return
		}

		log.Printf("ALLOWED %s/%s@%s", ecosystem, meta.Name, meta.Version)
	}

	forward(w, r, upstream, "/"+ecosystem)
}

const cacheTTL = 5 * time.Minute

func (h *Handler) cachedFetch(r *http.Request, reg registries.Registry, pkg *registries.PackageMeta, upstream, ecosystem string) (*registries.PackageMeta, error) {
	key := ecosystem + "/" + pkg.Name

	if v, ok := h.cache.Load(key); ok {
		entry := v.(*cacheEntry)
		if time.Now().Before(entry.expiresAt) {
			return entry.meta, nil
		}
		h.evictExpired(key, entry, time.Now())
	}

	meta, err := reg.FetchMetadata(r.Context(), pkg, upstream)
	if err != nil {
		return nil, err
	}

	entry := &cacheEntry{meta: meta, expiresAt: time.Now().Add(cacheTTL)}
	h.cache.Store(key, entry)
	time.AfterFunc(time.Until(entry.expiresAt), func() {
		h.evictExpired(key, entry, time.Now())
	})
	return meta, nil
}

func (h *Handler) evictExpired(key string, entry *cacheEntry, now time.Time) bool {
	if now.Before(entry.expiresAt) {
		return false
	}
	return h.cache.CompareAndDelete(key, entry)
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
