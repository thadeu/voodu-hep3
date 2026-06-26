// Package exporter serves the NDJSON tail of the shared capture volume —
// voodu-hep3's read path when HEP_STORE=ndjson. It is a dumb, cursor-based
// byte tailer over the sip-<hour>.ndjson files clowk-hep3 writes: it never
// parses or queries. Querying (calls, ladder, stats) happens downstream in
// the webui's SQLite, fed by a poller that pulls GET /export?since=<cursor>.
package exporter

import (
	"bufio"
	"bytes"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

const (
	filePrefix = "sip-"
	fileSuffix = ".ndjson"

	// maxExportBytes soft-caps one /export response so a cold poller doing
	// the initial backfill pages forward instead of buffering everything.
	maxExportBytes = 8 << 20 // 8 MiB
)

// Exporter tails the NDJSON files under dir.
type Exporter struct {
	dir string
}

// New builds an Exporter over the shared data directory.
func New(dir string) *Exporter {
	return &Exporter{dir: dir}
}

// Handler serves /export (cursor tail) and /health.
func (e *Exporter) Handler() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("/health", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, "ok\n")
	})

	mux.HandleFunc("/export", e.handleExport)

	return mux
}

func (e *Exporter) handleExport(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)

		return
	}

	since := r.URL.Query().Get("since")

	// Buffer (soft-capped) so the new cursor can go in a header before the
	// body — the poller stores it for the next ?since=.
	var buf bytes.Buffer

	cursor, err := e.Export(&buf, since, maxExportBytes)
	if err != nil {
		http.Error(w, "export: "+err.Error(), http.StatusInternalServerError)

		return
	}

	w.Header().Set("Content-Type", "application/x-ndjson")
	w.Header().Set("X-Hep-Cursor", cursor)
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(buf.Bytes())
}

// Export writes complete NDJSON lines newer than `since` to w, up to ~budget
// bytes, and returns the new cursor (file:offset) to resume from. A partial
// trailing line in the actively-written file is never emitted. Files
// removed by retention are skipped (the cursor resumes at the next survivor).
func (e *Exporter) Export(w io.Writer, since string, budget int64) (string, error) {
	files, err := e.listFiles()
	if err != nil {
		return since, err
	}

	if len(files) == 0 {
		return since, nil
	}

	startFile, startOff := parseCursor(since)

	idx := 0

	if startFile != "" {
		idx = sort.SearchStrings(files, startFile)

		// Cursor's file is gone (retention) or sorts before the first
		// survivor — resume at the next available file from the start.
		if idx >= len(files) || files[idx] != startFile {
			startOff = 0
		}
	}

	cursor := since
	remaining := budget

	for i := idx; i < len(files); i++ {
		name := files[i]

		off := int64(0)
		if name == startFile {
			off = startOff
		}

		newOff, written, err := e.streamFile(w, name, off, remaining)
		if err != nil {
			return cursor, err
		}

		cursor = makeCursor(name, newOff)
		remaining -= written

		if remaining <= 0 {
			break
		}
	}

	return cursor, nil
}

// streamFile copies complete lines from name (starting at off) to w until
// EOF or the budget is spent, returning the offset after the last complete
// line and the bytes written. A trailing partial line is left for next time.
func (e *Exporter) streamFile(w io.Writer, name string, off, budget int64) (int64, int64, error) {
	// #nosec G304 -- name comes from our own dir listing (fixed prefix/suffix).
	f, err := os.Open(filepath.Join(e.dir, name))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return off, 0, nil // raced with a retention delete
		}

		return off, 0, err
	}

	defer func() { _ = f.Close() }()

	if off > 0 {
		if _, err := f.Seek(off, io.SeekStart); err != nil {
			return off, 0, err
		}
	}

	r := bufio.NewReader(f)
	pos := off

	var written int64

	for written < budget {
		line, err := r.ReadBytes('\n')
		if errors.Is(err, io.EOF) {
			break // trailing partial line — don't emit until it's complete
		}

		if err != nil {
			return pos, written, err
		}

		if _, werr := w.Write(line); werr != nil {
			return pos, written, werr
		}

		n := int64(len(line))
		pos += n
		written += n
	}

	return pos, written, nil
}

func (e *Exporter) listFiles() ([]string, error) {
	ents, err := os.ReadDir(e.dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil // volume not populated yet
		}

		return nil, err
	}

	var names []string

	for _, en := range ents {
		n := en.Name()
		if !en.IsDir() && strings.HasPrefix(n, filePrefix) && strings.HasSuffix(n, fileSuffix) {
			names = append(names, n)
		}
	}

	sort.Strings(names)

	return names, nil
}

func makeCursor(file string, off int64) string {
	return file + ":" + strconv.FormatInt(off, 10)
}

func parseCursor(s string) (string, int64) {
	s = strings.TrimSpace(s)
	if s == "" {
		return "", 0
	}

	i := strings.LastIndex(s, ":")
	if i < 0 {
		return s, 0
	}

	off, err := strconv.ParseInt(s[i+1:], 10, 64)
	if err != nil || off < 0 {
		off = 0
	}

	return s[:i], off
}
