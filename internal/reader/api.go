package reader

import (
	"encoding/json"
	"fmt"
	"mime"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// MediaType is the vendor media type the API speaks. Clients select an
// API version through its `version` parameter on the Accept header:
//
//	Accept: application/vnd.clowk.hep+json;version=1
//
// Versioning by media type (not URL path) keeps the routes clean
// (/calls, /stats) and lets the response shape evolve to v2 without new
// paths. A generic Accept (*/*, application/json, none) gets the current
// version; an explicit unsupported version gets 406.
const MediaType = "application/vnd.clowk.hep+json"

// CurrentVersion is the latest API version; supportedVersions gates which
// versions a client may request.
const CurrentVersion = 1

var supportedVersions = map[int]bool{1: true}

// API serves the read endpoints over the Reader.
type API struct {
	reader *Reader
	// Now is injected for deterministic tests of default time windows.
	Now func() time.Time
}

// NewAPI builds an API over the given Reader.
func NewAPI(r *Reader) *API {
	return &API{reader: r, Now: time.Now}
}

// Handler returns the mux. Routes are version-agnostic; the version is
// negotiated per request from the Accept header.
func (a *API) Handler() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /health", a.handleHealth)
	mux.HandleFunc("GET /calls", a.versioned(a.handleCalls))
	mux.HandleFunc("GET /calls/{id}", a.versioned(a.handleCall))
	mux.HandleFunc("GET /stats", a.versioned(a.handleStats))

	return mux
}

func (a *API) now() time.Time {
	if a.Now != nil {
		return a.Now()
	}

	return time.Now()
}

// versioned wraps a handler with content negotiation: it resolves the
// requested API version (406 on an explicit unsupported one) and stamps
// the response Content-Type with the negotiated version.
func (a *API) versioned(next func(http.ResponseWriter, *http.Request, int)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		version, ok := negotiateVersion(r.Header.Get("Accept"))
		if !ok {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusNotAcceptable)
			_ = json.NewEncoder(w).Encode(map[string]string{
				"error": fmt.Sprintf("unsupported API version; this server speaks %s;version=%d", MediaType, CurrentVersion),
			})

			return
		}

		w.Header().Set("Content-Type", fmt.Sprintf("%s;version=%d", MediaType, version))
		next(w, r, version)
	}
}

// negotiateVersion resolves the API version from an Accept header value.
// Returns (version, true) on success; (0, false) when the client
// explicitly asked for our media type with an unsupported version.
// Anything that doesn't name our media type (incl. empty, */*,
// application/json) resolves to the current version.
func negotiateVersion(accept string) (int, bool) {
	if strings.TrimSpace(accept) == "" {
		return CurrentVersion, true
	}

	for _, part := range strings.Split(accept, ",") {
		mt, params, err := mime.ParseMediaType(strings.TrimSpace(part))
		if err != nil {
			continue
		}

		if mt != MediaType {
			continue
		}

		v := params["version"]
		if v == "" {
			return CurrentVersion, true
		}

		n, err := strconv.Atoi(v)
		if err != nil || !supportedVersions[n] {
			return 0, false
		}

		return n, true
	}

	// Accept present but never names our vendor type → be lenient.
	return CurrentVersion, true
}

func (a *API) handleHealth(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// handleCalls serves GET /calls?from=&until=&q=&page=&per_page=.
func (a *API) handleCalls(w http.ResponseWriter, r *http.Request, _ int) {
	from, until := a.window(r, time.Hour)

	q := r.URL.Query().Get("q")
	perPage := queryInt(r, "per_page", 50)
	page := queryInt(r, "page", 1)

	if page < 1 {
		page = 1
	}

	calls, err := a.reader.ListCalls(r.Context(), from, until, q, perPage, (page-1)*perPage)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)

		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"calls": calls,
		"count": len(calls),
		"page":  page,
		"from":  from,
		"until": until,
	})
}

// handleCall serves GET /calls/{id} — the ladder diagram source.
func (a *API) handleCall(w http.ResponseWriter, r *http.Request, _ int) {
	id := r.PathValue("id")
	if id == "" {
		writeErr(w, http.StatusBadRequest, fmt.Errorf("call id is required"))

		return
	}

	msgs, err := a.reader.GetCall(r.Context(), id)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)

		return
	}

	if len(msgs) == 0 {
		writeJSON(w, http.StatusNotFound, map[string]any{"id": id, "messages": []SipMessage{}, "count": 0})

		return
	}

	writeJSON(w, http.StatusOK, map[string]any{"id": id, "messages": msgs, "count": len(msgs)})
}

// handleStats serves GET /stats?from=&until=&interval=.
func (a *API) handleStats(w http.ResponseWriter, r *http.Request, _ int) {
	from, until := a.window(r, time.Hour)

	interval := time.Minute

	if v := r.URL.Query().Get("interval"); v != "" {
		if d, err := time.ParseDuration(v); err == nil && d > 0 {
			interval = d
		}
	}

	points, err := a.reader.Stats(r.Context(), from, until, interval)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)

		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"stats":    points,
		"count":    len(points),
		"from":     from,
		"until":    until,
		"interval": interval.String(),
	})
}

// window resolves the [from, until] query window (RFC3339; until defaults
// to now, from to until-fallback).
func (a *API) window(r *http.Request, fallback time.Duration) (from, until time.Time) {
	q := r.URL.Query()

	until = a.now()

	if v := q.Get("until"); v != "" {
		if t, err := time.Parse(time.RFC3339, v); err == nil {
			until = t
		}
	}

	from = until.Add(-fallback)

	if v := q.Get("from"); v != "" {
		if t, err := time.Parse(time.RFC3339, v); err == nil {
			from = t
		}
	}

	return from, until
}

func queryInt(r *http.Request, key string, def int) int {
	v := r.URL.Query().Get(key)
	if v == "" {
		return def
	}

	n, err := strconv.Atoi(v)
	if err != nil {
		return def
	}

	return n
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	if w.Header().Get("Content-Type") == "" {
		w.Header().Set("Content-Type", "application/json")
	}

	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, status int, err error) {
	writeJSON(w, status, map[string]string{"error": err.Error()})
}
