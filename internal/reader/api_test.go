package reader

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// /calls returns JSON. Gated on Postgres (the handler queries).
func TestAPI_CallsReturnsJSON(t *testing.T) {
	r := readerDB(t)

	a := NewAPI(r)
	a.Now = func() time.Time { return base.Add(time.Minute) }

	srv := httptest.NewServer(a.Handler())
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/calls") //nolint:noctx // test
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, want 200 (%s)", resp.StatusCode, body)
	}

	if ct := resp.Header.Get("Content-Type"); !strings.Contains(ct, "application/json") {
		t.Errorf("Content-Type = %q, want application/json", ct)
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
