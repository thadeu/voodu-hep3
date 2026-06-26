package exporter

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeFile(t *testing.T, dir, name string, lines ...string) {
	t.Helper()

	var b strings.Builder

	for _, l := range lines {
		b.WriteString(l)
		b.WriteByte('\n')
	}

	if err := os.WriteFile(filepath.Join(dir, name), []byte(b.String()), 0o600); err != nil {
		t.Fatal(err)
	}
}

func appendRaw(t *testing.T, dir, name, raw string) {
	t.Helper()

	// #nosec G304 -- test-local temp dir.
	f, err := os.OpenFile(filepath.Join(dir, name), os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		t.Fatal(err)
	}

	defer func() { _ = f.Close() }()

	if _, err := f.WriteString(raw); err != nil {
		t.Fatal(err)
	}
}

func nonEmptyLines(s string) []string {
	var out []string

	for _, l := range strings.Split(s, "\n") {
		if l != "" {
			out = append(out, l)
		}
	}

	return out
}

// An empty cursor exports everything, in chronological (file-sorted) order.
func TestExport_AllFromEmpty(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "sip-2026-06-26T10.ndjson", `{"call_id":"a"}`, `{"call_id":"b"}`)
	writeFile(t, dir, "sip-2026-06-26T11.ndjson", `{"call_id":"c"}`)

	var buf bytes.Buffer

	cur, err := New(dir).Export(&buf, "", maxExportBytes)
	if err != nil {
		t.Fatal(err)
	}

	if got := nonEmptyLines(buf.String()); len(got) != 3 {
		t.Fatalf("want 3 lines, got %d: %q", len(got), buf.String())
	}

	if !strings.HasPrefix(cur, "sip-2026-06-26T11.ndjson:") {
		t.Errorf("cursor should point at the last file, got %q", cur)
	}
}

// Re-exporting from the returned cursor yields nothing until new data is
// appended, then yields only the new line.
func TestExport_Incremental(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "sip-2026-06-26T10.ndjson", `{"call_id":"a"}`)

	e := New(dir)

	var first bytes.Buffer

	cur, err := e.Export(&first, "", maxExportBytes)
	if err != nil {
		t.Fatal(err)
	}

	var caughtUp bytes.Buffer

	cur2, err := e.Export(&caughtUp, cur, maxExportBytes)
	if err != nil {
		t.Fatal(err)
	}

	if caughtUp.Len() != 0 {
		t.Errorf("caught-up export should be empty, got %q", caughtUp.String())
	}

	appendRaw(t, dir, "sip-2026-06-26T10.ndjson", `{"call_id":"b"}`+"\n")

	var delta bytes.Buffer

	if _, err := e.Export(&delta, cur2, maxExportBytes); err != nil {
		t.Fatal(err)
	}

	if got := nonEmptyLines(delta.String()); len(got) != 1 || !strings.Contains(got[0], `"b"`) {
		t.Errorf("incremental export should return only the new line, got %q", delta.String())
	}
}

// A trailing line without a newline (the actively-written file) is not
// emitted until it is completed.
func TestExport_PartialLineNotEmitted(t *testing.T) {
	dir := t.TempDir()
	name := "sip-2026-06-26T10.ndjson"

	// One complete line + a partial (no trailing newline).
	if err := os.WriteFile(filepath.Join(dir, name), []byte(`{"call_id":"a"}`+"\n"+`{"call_id":"b"`), 0o600); err != nil {
		t.Fatal(err)
	}

	e := New(dir)

	var buf bytes.Buffer

	cur, err := e.Export(&buf, "", maxExportBytes)
	if err != nil {
		t.Fatal(err)
	}

	if got := nonEmptyLines(buf.String()); len(got) != 1 {
		t.Fatalf("partial line must be withheld; got %d lines: %q", len(got), buf.String())
	}

	// Complete the partial line; now it should come through.
	appendRaw(t, dir, name, "}\n")

	var buf2 bytes.Buffer

	if _, err := e.Export(&buf2, cur, maxExportBytes); err != nil {
		t.Fatal(err)
	}

	if got := nonEmptyLines(buf2.String()); len(got) != 1 || !strings.Contains(got[0], `"b"`) {
		t.Errorf("completed line should be emitted next, got %q", buf2.String())
	}
}

// When the cursor's file was deleted by retention, the export resumes from
// the next surviving file (no error, no stall).
func TestExport_DeletedCursorFileResumes(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "sip-2026-06-26T10.ndjson", `{"call_id":"a"}`)
	writeFile(t, dir, "sip-2026-06-26T11.ndjson", `{"call_id":"c"}`)

	// Cursor mid-way through the (about to be deleted) first file.
	stale := makeCursor("sip-2026-06-26T10.ndjson", 5)

	if err := os.Remove(filepath.Join(dir, "sip-2026-06-26T10.ndjson")); err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer

	cur, err := New(dir).Export(&buf, stale, maxExportBytes)
	if err != nil {
		t.Fatal(err)
	}

	if got := nonEmptyLines(buf.String()); len(got) != 1 || !strings.Contains(got[0], `"c"`) {
		t.Errorf("should resume at the surviving file, got %q", buf.String())
	}

	if !strings.HasPrefix(cur, "sip-2026-06-26T11.ndjson:") {
		t.Errorf("cursor should advance to the survivor, got %q", cur)
	}
}

// The byte budget caps one response; the cursor lets the caller continue.
func TestExport_BudgetCap(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "sip-2026-06-26T10.ndjson", `{"n":1}`, `{"n":2}`, `{"n":3}`)

	e := New(dir)

	var first bytes.Buffer

	cur, err := e.Export(&first, "", 1) // budget of 1 byte → ~1 line
	if err != nil {
		t.Fatal(err)
	}

	if got := nonEmptyLines(first.String()); len(got) != 1 {
		t.Fatalf("budget cap should yield 1 line, got %d: %q", len(got), first.String())
	}

	var rest bytes.Buffer

	if _, err := e.Export(&rest, cur, maxExportBytes); err != nil {
		t.Fatal(err)
	}

	if got := nonEmptyLines(rest.String()); len(got) != 2 {
		t.Errorf("continuation should yield the remaining 2 lines, got %d: %q", len(got), rest.String())
	}
}

// The HTTP handler streams the body and returns the cursor in a header.
func TestHandler_ExportAndHealth(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "sip-2026-06-26T10.ndjson", `{"call_id":"a"}`)

	ts := httptest.NewServer(New(dir).Handler())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/health")
	if err != nil {
		t.Fatal(err)
	}

	_ = resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("/health = %d, want 200", resp.StatusCode)
	}

	resp, err = http.Get(ts.URL + "/export")
	if err != nil {
		t.Fatal(err)
	}

	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("/export = %d, want 200", resp.StatusCode)
	}

	if cur := resp.Header.Get("X-Hep-Cursor"); !strings.HasPrefix(cur, "sip-2026-06-26T10.ndjson:") {
		t.Errorf("X-Hep-Cursor = %q", cur)
	}

	body := new(bytes.Buffer)
	_, _ = body.ReadFrom(resp.Body)

	if !strings.Contains(body.String(), `"call_id":"a"`) {
		t.Errorf("body missing the line: %q", body.String())
	}
}
