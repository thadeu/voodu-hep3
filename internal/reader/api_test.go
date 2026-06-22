package reader

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestNegotiateVersion(t *testing.T) {
	tests := []struct {
		accept      string
		wantVersion int
		wantOK      bool
	}{
		{"", 1, true},
		{"*/*", 1, true},
		{"application/json", 1, true},
		{"text/html", 1, true}, // lenient: doesn't name our type → current
		{"application/vnd.clowk.hep+json", 1, true},
		{"application/vnd.clowk.hep+json;version=1", 1, true},
		{"text/html, application/vnd.clowk.hep+json;version=1", 1, true},
		{"application/vnd.clowk.hep+json;version=2", 0, false}, // unsupported
		{"application/vnd.clowk.hep+json;version=abc", 0, false},
	}

	for _, tt := range tests {
		t.Run(tt.accept, func(t *testing.T) {
			v, ok := negotiateVersion(tt.accept)

			if ok != tt.wantOK || (ok && v != tt.wantVersion) {
				t.Errorf("negotiateVersion(%q) = (%d, %v), want (%d, %v)", tt.accept, v, ok, tt.wantVersion, tt.wantOK)
			}
		})
	}
}

// An explicit unsupported version → 406 with a clear message.
func TestAPI_UnsupportedVersion406(t *testing.T) {
	a := &API{Now: func() time.Time { return base }} // no reader needed; 406 happens before query

	srv := httptest.NewServer(a.Handler())
	defer srv.Close()

	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/calls", nil)
	req.Header.Set("Accept", "application/vnd.clowk.hep+json;version=99")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusNotAcceptable {
		t.Errorf("status = %d, want 406", resp.StatusCode)
	}
}

// With a supported version, the response Content-Type echoes it. Gated on
// Postgres (the handler queries).
func TestAPI_CallsContentType(t *testing.T) {
	r := readerDB(t)

	a := NewAPI(r)
	a.Now = func() time.Time { return base.Add(time.Minute) }

	srv := httptest.NewServer(a.Handler())
	defer srv.Close()

	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/calls", nil)
	req.Header.Set("Accept", "application/vnd.clowk.hep+json;version=1")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, want 200 (%s)", resp.StatusCode, body)
	}

	ct := resp.Header.Get("Content-Type")
	if !strings.Contains(ct, MediaType) || !strings.Contains(ct, "version=1") {
		t.Errorf("Content-Type = %q, want %s;version=1", ct, MediaType)
	}
}

func TestAPI_Health(t *testing.T) {
	a := &API{Now: time.Now}

	srv := httptest.NewServer(a.Handler())
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/health") //nolint:noctx // test
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("health = %d, want 200", resp.StatusCode)
	}
}
