package reader

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"
)

// testSchema mirrors clowk-hep3's migration (the cross-project contract).
// The reader doesn't own migrations, so its tests create the schema to
// run against a blank Postgres.
const testSchema = `
CREATE TABLE IF NOT EXISTS sip_messages (
  id            BIGSERIAL PRIMARY KEY,
  data          JSONB NOT NULL,
  ts            TEXT    GENERATED ALWAYS AS (data->>'ts')                  STORED,
  call_id       TEXT    GENERATED ALWAYS AS (data->>'call_id')             STORED,
  x_cid         TEXT    GENERATED ALWAYS AS (data->>'x_cid')               STORED,
  method        TEXT    GENERATED ALWAYS AS (data->>'method')              STORED,
  response_code INTEGER GENERATED ALWAYS AS ((data->>'response_code')::int) STORED,
  from_user     TEXT    GENERATED ALWAYS AS (data->>'from_user')           STORED,
  to_user       TEXT    GENERATED ALWAYS AS (data->>'to_user')             STORED,
  cseq          TEXT    GENERATED ALWAYS AS (data->>'cseq')                STORED,
  created_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);`

var base = time.Date(2026, 6, 21, 12, 0, 0, 0, time.UTC)

func readerDB(t *testing.T) *Reader {
	t.Helper()

	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("set TEST_DATABASE_URL to run Postgres-backed reader tests")
	}

	r, err := NewReader(url)
	if err != nil {
		t.Fatalf("NewReader: %v", err)
	}

	if _, err := r.DB().Exec(testSchema); err != nil {
		t.Fatalf("ensure schema: %v", err)
	}

	if _, err := r.DB().Exec("TRUNCATE sip_messages"); err != nil {
		t.Fatalf("truncate: %v", err)
	}

	t.Cleanup(func() { _ = r.Close() })

	return r
}

// rec builds a stored JSON record at ts with the given fields.
func rec(ts time.Time, callID, xcid, method string, code int, cseq string) jsonRecord {
	return jsonRecord{TS: formatTS(ts), CallID: callID, XCID: xcid, Method: method, ResponseCode: code, CSeq: cseq}
}

func seed(t *testing.T, r *Reader, recs ...jsonRecord) {
	t.Helper()

	for _, rc := range recs {
		raw, _ := json.Marshal(rc)

		if _, err := r.DB().Exec(`INSERT INTO sip_messages (data) VALUES ($1::jsonb)`, string(raw)); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}
}

func TestListCalls_B2BUACollapsesByXCID(t *testing.T) {
	r := readerDB(t)

	seed(t, r,
		jsonRecord{TS: formatTS(base), CallID: "legA", XCID: "corr", Method: "INVITE", FromUser: "alice", ToUser: "555"},
		jsonRecord{TS: formatTS(base.Add(time.Second)), CallID: "legB", XCID: "corr", Method: "INVITE", FromUser: "alice", ToUser: "555"},
		jsonRecord{TS: formatTS(base.Add(2 * time.Second)), CallID: "legB", XCID: "corr", ResponseCode: 200},
	)

	calls, err := r.ListCalls(context.Background(), base.Add(-time.Hour), base.Add(time.Hour), "", 50, 0)
	if err != nil {
		t.Fatalf("ListCalls: %v", err)
	}

	if len(calls) != 1 {
		t.Fatalf("got %d calls, want 1 (two legs, one X-CID)", len(calls))
	}

	if calls[0].ID != "corr" || calls[0].Messages != 3 || calls[0].Status != "answered" {
		t.Errorf("call = %+v, want id=corr messages=3 status=answered", calls[0])
	}
}

func TestListCalls_StatusAndFilter(t *testing.T) {
	r := readerDB(t)

	seed(t, r,
		rec(base, "a", "", "INVITE", 0, ""), rec(base.Add(time.Second), "a", "", "", 200, ""),
		rec(base, "b", "", "INVITE", 0, ""), rec(base.Add(time.Second), "b", "", "", 486, ""),
	)
	// give 'a' a searchable user
	seed(t, r, jsonRecord{TS: formatTS(base.Add(2 * time.Second)), CallID: "a", FromUser: "needle"})

	calls, err := r.ListCalls(context.Background(), base.Add(-time.Hour), base.Add(time.Hour), "needle", 50, 0)
	if err != nil {
		t.Fatalf("ListCalls: %v", err)
	}

	if len(calls) != 1 || calls[0].ID != "a" || calls[0].Status != "answered" {
		t.Errorf("q filter/status wrong: %+v", calls)
	}
}

func TestGetCall_ResolvesB2BUAOrderedAsc(t *testing.T) {
	r := readerDB(t)

	seed(t, r,
		jsonRecord{TS: formatTS(base.Add(2 * time.Second)), CallID: "legB", XCID: "corr", ResponseCode: 200},
		jsonRecord{TS: formatTS(base), CallID: "legA", XCID: "corr", Method: "INVITE"},
		jsonRecord{TS: formatTS(base.Add(3 * time.Second)), CallID: "legA", XCID: "", Method: "ACK"},
		jsonRecord{TS: formatTS(base.Add(time.Second)), CallID: "legB", XCID: "corr", Method: "INVITE"},
	)

	msgs, err := r.GetCall(context.Background(), "corr")
	if err != nil {
		t.Fatalf("GetCall: %v", err)
	}

	if len(msgs) != 4 {
		t.Fatalf("got %d, want 4 (both legs incl. header-less ACK)", len(msgs))
	}

	for i := 1; i < len(msgs); i++ {
		if msgs[i].TS.Before(msgs[i-1].TS) {
			t.Errorf("not ascending at %d", i)
		}
	}

	if msgs[0].Method != "INVITE" || !msgs[0].TS.Equal(base) {
		t.Errorf("first = %q@%v, want INVITE@base", msgs[0].Method, msgs[0].TS)
	}
}

// Active gauge: rises on a 2xx answer to an INVITE, falls on a BYE; the
// 200 to the BYE (cseq BYE) must NOT re-add.
func TestStats_ActiveCalls(t *testing.T) {
	r := readerDB(t)

	seed(t, r,
		rec(base, "a", "", "INVITE", 0, "1 INVITE"),
		rec(base.Add(1*time.Second), "a", "", "", 200, "1 INVITE"),
		rec(base.Add(2*time.Second), "b", "", "INVITE", 0, "1 INVITE"),
		rec(base.Add(3*time.Second), "b", "", "", 200, "1 INVITE"),
		rec(base.Add(4*time.Second), "a", "", "BYE", 0, "2 BYE"),
		rec(base.Add(5*time.Second), "a", "", "", 200, "2 BYE"),
	)

	points, err := r.Stats(context.Background(), base.Add(-time.Minute), base.Add(time.Minute), time.Second)
	if err != nil {
		t.Fatalf("Stats: %v", err)
	}

	var maxActive, lastActive int

	for _, p := range points {
		if p.Active > maxActive {
			maxActive = p.Active
		}

		lastActive = p.Active
	}

	if maxActive != 2 {
		t.Errorf("peak active = %d, want 2", maxActive)
	}

	if lastActive != 1 {
		t.Errorf("final active = %d, want 1 (A ended, B talking; BYE-200 must not re-add)", lastActive)
	}
}

func TestStats_IntervalChangesBucketing(t *testing.T) {
	r := readerDB(t)

	seed(t, r,
		rec(base, "1", "", "INVITE", 0, ""),
		rec(base.Add(30*time.Second), "2", "", "INVITE", 0, ""),
		rec(base.Add(90*time.Second), "3", "", "INVITE", 0, ""),
	)

	from, until := base.Add(-time.Minute), base.Add(10*time.Minute)

	oneMin, err := r.Stats(context.Background(), from, until, time.Minute)
	if err != nil {
		t.Fatalf("Stats 1m: %v", err)
	}

	thirtySec, err := r.Stats(context.Background(), from, until, 30*time.Second)
	if err != nil {
		t.Fatalf("Stats 30s: %v", err)
	}

	if len(oneMin) != 2 {
		t.Errorf("1m: got %d buckets, want 2", len(oneMin))
	}

	if len(thirtySec) != 3 {
		t.Errorf("30s: got %d buckets, want 3", len(thirtySec))
	}
}
