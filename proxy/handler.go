package proxy

import (
	"fmt"
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
	"sort"
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
	index     *registries.VersionIndex
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
	case strings.HasPrefix(r.URL.Path, "/npm/"):
		h.handle(recorder, r, registries.NPM{}, "npm")
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

	if pkg != nil && !isOverrideAllowAll(h.cfg, r) {
		index, err := h.cachedFetch(r, reg, pkg, upstream, ecosystem)
		if err != nil {
			http.Error(w, "pkgwatch: metadata error: "+err.Error(), http.StatusBadGateway)
			return
		}

		allowed := make(map[string]bool, len(index.Versions))
		blockedVersions := make([]string, 0)
		var firstViolation string
		for version, meta := range index.Versions {
			if violation := checkRules(h.cfg, meta); violation != "" {
				blockedVersions = append(blockedVersions, version)
				if firstViolation == "" {
					firstViolation = violation
				}
				continue
			}
			allowed[version] = true
		}

		if pkg.Version != "" && !allowed[pkg.Version] {
			violation := firstViolation
			if meta := index.Versions[pkg.Version]; meta != nil {
				violation = checkRules(h.cfg, meta)
			}
			if violation == "" {
				violation = fmt.Sprintf("%s@%s is unavailable under policy", pkg.Name, pkg.Version)
			}
			log.Printf("BLOCKED %s/%s@%s: %s", ecosystem, pkg.Name, pkg.Version, violation)
			http.Error(w, "pkgwatch: blocked — "+violation, http.StatusForbidden)
			return
		}

		if len(allowed) == 0 {
			log.Printf("BLOCKED %s/%s: %s", ecosystem, pkg.Name, firstViolation)
			http.Error(w, "pkgwatch: blocked — "+firstViolation, http.StatusForbidden)
			return
		}

		if isPackageIndexRequest(ecosystem, r.URL.Path) {
			sort.Strings(blockedVersions)
			modify := func(resp *http.Response) error {
				if len(blockedVersions) > 0 {
					resp.Header.Set("X-Pkgwatch-Blocked-Versions", strings.Join(blockedVersions, ","))
					log.Printf("FILTERED %s/%s versions: %s", ecosystem, pkg.Name, strings.Join(blockedVersions, ", "))
				}
				switch ecosystem {
				case "npm":
					return (registries.NPM{}).FilterResponse(resp, allowed)
				case "pypi":
					return (registries.PyPI{}).FilterResponse(resp, index.Files, allowed)
				default:
					return nil
				}
			}
			if err := forward(w, r, upstream, "/"+ecosystem, ecosystem == "pypi", modify); err != nil {
				http.Error(w, "pkgwatch: invalid upstream: "+err.Error(), http.StatusInternalServerError)
			}
			return
		}

		log.Printf("ALLOWED %s/%s", ecosystem, pkg.Name)
	}

	if err := forward(w, r, upstream, "/"+ecosystem, false, nil); err != nil {
		http.Error(w, "pkgwatch: invalid upstream: "+err.Error(), http.StatusInternalServerError)
	}
}

const cacheTTL = 5 * time.Minute

func (h *Handler) cachedFetch(r *http.Request, reg registries.Registry, pkg *registries.PackageMeta, upstream, ecosystem string) (*registries.VersionIndex, error) {
	key := ecosystem + "/" + pkg.Name

	if v, ok := h.cache.Load(key); ok {
		entry := v.(*cacheEntry)
		if time.Now().Before(entry.expiresAt) {
			return entry.index, nil
		}
		h.evictExpired(key, entry, time.Now())
	}

	index, err := reg.FetchVersionIndex(r.Context(), pkg, upstream)
	if err != nil {
		return nil, err
	}

	entry := &cacheEntry{index: index, expiresAt: time.Now().Add(cacheTTL)}
	h.cache.Store(key, entry)
	time.AfterFunc(time.Until(entry.expiresAt), func() {
		h.evictExpired(key, entry, time.Now())
	})
	return index, nil
}

func (h *Handler) evictExpired(key string, entry *cacheEntry, now time.Time) bool {
	if now.Before(entry.expiresAt) {
		return false
	}
	return h.cache.CompareAndDelete(key, entry)
}

func forward(w http.ResponseWriter, r *http.Request, upstream, stripPrefix string, pypiJSON bool, modify func(*http.Response) error) error {
	target, err := url.Parse(upstream)
	if err != nil || target.Scheme == "" || target.Host == "" {
		return fmt.Errorf("malformed upstream URL %q", upstream)
	}
	rp := &httputil.ReverseProxy{}
	rp.Rewrite = func(proxyReq *httputil.ProxyRequest) {
		proxyReq.Out.URL.Path = strings.TrimPrefix(proxyReq.Out.URL.Path, stripPrefix)
		if proxyReq.Out.URL.RawPath != "" {
			proxyReq.Out.URL.RawPath = strings.TrimPrefix(proxyReq.Out.URL.RawPath, stripPrefix)
		}
		proxyReq.SetURL(target)
		proxyReq.SetXForwarded()
		proxyReq.Out.Host = target.Host
		if pypiJSON {
			proxyReq.Out.Header.Set("Accept", "application/vnd.pypi.simple.v1+json")
		}
		proxyReq.Out.Header.Del("Accept-Encoding")
	}
	rp.ModifyResponse = modify
	rp.ErrorHandler = func(rw http.ResponseWriter, req *http.Request, proxyErr error) {
		log.Printf("UPSTREAM ERROR %s %s: %v", req.Method, req.URL.Path, proxyErr)
		http.Error(rw, "pkgwatch: upstream request failed", http.StatusBadGateway)
	}
	rp.ServeHTTP(w, r)
	return nil
}

func isPackageIndexRequest(ecosystem, path string) bool {
	path = strings.Trim(strings.TrimPrefix(path, "/"+ecosystem+"/"), "/")
	parts := strings.Split(path, "/")
	switch ecosystem {
	case "npm":
		return len(parts) == 1 || (len(parts) == 2 && strings.HasPrefix(parts[0], "@"))
	case "pypi":
		return len(parts) == 2 && parts[0] == "simple"
	default:
		return false
	}
}

func checkRules(cfg *config.Policy, meta *registries.PackageMeta) string {
	if cfg.Rules.BlockPrerelease && meta.IsPrerelease {
		return fmt.Sprintf("%s@%s is a pre-release (policy blocks pre-releases)", meta.Name, meta.Version)
	}
	if cfg.Rules.MinAgeDays > 0 {
		ageDays := int(time.Since(meta.PublishedAt).Hours() / 24)
		if ageDays < cfg.Rules.MinAgeDays {
			return fmt.Sprintf("%s@%s is %d days old (policy requires %d)", meta.Name, meta.Version, ageDays, cfg.Rules.MinAgeDays)
		}
	}
	return ""
}

func isOverrideAllowAll(cfg *config.Policy, r *http.Request) bool {
	configured := strings.TrimSpace(cfg.Overrides.OverrideCommand)
	if configured == "" {
		return false
	}

	for key := range r.URL.Query() {
		if cfg.Overrides.AllowAll(key) {
			return true
		}
	}

	return cfg.Overrides.AllowAll(r.URL.Query().Get("override_command"))
}
