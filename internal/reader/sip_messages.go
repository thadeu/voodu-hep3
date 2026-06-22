package reader

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib" // database/sql driver "pgx"
	"github.com/jmoiron/sqlx"
)

// tsLayout MUST match clowk-hep3's writer (the `ts` column is stored as
// fixed-width ISO8601 UTC text). The two projects share no Go code — the
// contract between them is the database schema + this format.
const tsLayout = "2006-01-02 15:04:05.000000"

func formatTS(t time.Time) string { return t.UTC().Format(tsLayout) }
func parseTS(s string) time.Time  { t, _ := time.Parse(tsLayout, s); return t }

// SipMessage is one stored SIP message, read back for a ladder diagram.
type SipMessage struct {
	TS           time.Time `json:"ts"`
	CallID       string    `json:"call_id"`
	XCID         string    `json:"x_cid,omitempty"`
	Method       string    `json:"method,omitempty"`
	ResponseCode int       `json:"response_code,omitempty"`
	FromUser     string    `json:"from_user,omitempty"`
	ToUser       string    `json:"to_user,omitempty"`
	RURI         string    `json:"ruri,omitempty"`
	SrcIP        string    `json:"src_ip,omitempty"`
	DstIP        string    `json:"dst_ip,omitempty"`
	SrcPort      int       `json:"src_port,omitempty"`
	DstPort      int       `json:"dst_port,omitempty"`
	NodeID       int       `json:"node_id,omitempty"`
	UserAgent    string    `json:"user_agent,omitempty"`
	CSeq         string    `json:"cseq,omitempty"`
	RawSIP       string    `json:"raw_sip,omitempty"`
}

// jsonRecord mirrors the JSONB document clowk-hep3 stores in `data`.
type jsonRecord struct {
	TS           string `json:"ts"`
	CallID       string `json:"call_id"`
	XCID         string `json:"x_cid"`
	Method       string `json:"method"`
	ResponseCode int    `json:"response_code"`
	FromUser     string `json:"from_user"`
	ToUser       string `json:"to_user"`
	RURI         string `json:"ruri"`
	SrcIP        string `json:"src_ip"`
	DstIP        string `json:"dst_ip"`
	SrcPort      int    `json:"src_port"`
	DstPort      int    `json:"dst_port"`
	NodeID       int    `json:"node_id"`
	UserAgent    string `json:"user_agent"`
	CSeq         string `json:"cseq"`
	RawSIP       string `json:"raw_sip"`
}

func (r jsonRecord) toMessage() SipMessage {
	return SipMessage{
		TS: parseTS(r.TS), CallID: r.CallID, XCID: r.XCID, Method: r.Method,
		ResponseCode: r.ResponseCode, FromUser: r.FromUser, ToUser: r.ToUser,
		RURI: r.RURI, SrcIP: r.SrcIP, DstIP: r.DstIP, SrcPort: r.SrcPort,
		DstPort: r.DstPort, NodeID: r.NodeID, UserAgent: r.UserAgent,
		CSeq: r.CSeq, RawSIP: r.RawSIP,
	}
}

// CallSummary is one row in the call list.
type CallSummary struct {
	ID         string    `json:"id"`
	CallID     string    `json:"call_id"`
	XCID       string    `json:"x_cid,omitempty"`
	FromUser   string    `json:"from_user"`
	ToUser     string    `json:"to_user"`
	StartedAt  time.Time `json:"started_at"`
	EndedAt    time.Time `json:"ended_at"`
	DurationMs int64     `json:"duration_ms"`
	Messages   int       `json:"messages"`
	Status     string    `json:"status"`
}

// StatPoint is one time bucket of counters. Active is the in-conversation
// gauge (answered INVITEs minus BYEs, running, clamped at 0).
type StatPoint struct {
	Bucket   time.Time `json:"bucket"`
	Invites  int       `json:"invites"`
	Answered int       `json:"answered"`
	Failed   int       `json:"failed"`
	Byes     int       `json:"byes"`
	Active   int       `json:"active"`
	Total    int       `json:"total"`
}

// Reader queries the shared Postgres. Read-only — no migrations, no writes.
type Reader struct {
	db *sqlx.DB
}

// NewReader connects to the shared Postgres.
func NewReader(databaseURL string) (*Reader, error) {
	db, err := sqlx.Connect("pgx", databaseURL)
	if err != nil {
		return nil, fmt.Errorf("connect postgres: %w", err)
	}

	db.SetMaxOpenConns(10)
	db.SetMaxIdleConns(5)
	db.SetConnMaxLifetime(5 * time.Minute)

	return &Reader{db: db}, nil
}

// Close closes the database handle.
func (r *Reader) Close() error { return r.db.Close() }

// DB exposes the handle for tests.
func (r *Reader) DB() *sqlx.DB { return r.db }

// ListCalls returns calls within [from, until], grouped by correlation
// key (x_cid when present, else call_id), newest-first.
func (r *Reader) ListCalls(ctx context.Context, from, until time.Time, q string, limit, offset int) ([]CallSummary, error) {
	if limit <= 0 {
		limit = 50
	}

	args := []any{formatTS(from), formatTS(until)}

	// q filters by GROUP, not by row: bool_or keeps a call when ANY of
	// its messages matches, while the aggregates still cover ALL the
	// call's rows. Row-level filtering would corrupt the status/counts of
	// a call matched on a field only some rows carry.
	having := ""

	if q != "" {
		args = append(args, "%"+q+"%")
		having = fmt.Sprintf(
			"HAVING bool_or(call_id ILIKE $%d OR x_cid ILIKE $%d OR from_user ILIKE $%d OR to_user ILIKE $%d)",
			len(args), len(args), len(args), len(args))
	}

	args = append(args, limit, offset)
	limitPos := len(args) - 1
	offsetPos := len(args)

	query := fmt.Sprintf(`
SELECT
  CASE WHEN COALESCE(x_cid,'') <> '' THEN x_cid ELSE call_id END AS corr_key,
  MAX(call_id)                AS any_call_id,
  COALESCE(MAX(x_cid),'')     AS any_x_cid,
  COALESCE(MAX(NULLIF(from_user,'')),'') AS from_user,
  COALESCE(MAX(NULLIF(to_user,'')),'')   AS to_user,
  MIN(ts)                     AS started_at,
  MAX(ts)                     AS ended_at,
  COUNT(*)                    AS messages,
  MAX(CASE WHEN response_code BETWEEN 200 AND 299 THEN 1 ELSE 0 END) AS answered,
  MAX(CASE WHEN response_code >= 400 THEN response_code ELSE 0 END)  AS max_final,
  MAX(CASE WHEN method = 'INVITE' THEN 1 ELSE 0 END) AS saw_invite
FROM sip_messages
WHERE ts BETWEEN $1 AND $2
GROUP BY corr_key
%s
ORDER BY started_at DESC
LIMIT $%d OFFSET $%d`, having, limitPos, offsetPos)

	var rows []callRow

	if err := r.db.SelectContext(ctx, &rows, query, args...); err != nil {
		return nil, fmt.Errorf("list calls: %w", err)
	}

	out := make([]CallSummary, 0, len(rows))

	for _, row := range rows {
		started := parseTS(row.StartedAt)
		ended := parseTS(row.EndedAt)

		out = append(out, CallSummary{
			ID:         row.CorrKey,
			CallID:     row.AnyCallID,
			XCID:       row.AnyXCID,
			FromUser:   row.FromUser,
			ToUser:     row.ToUser,
			StartedAt:  started,
			EndedAt:    ended,
			DurationMs: ended.Sub(started).Milliseconds(),
			Messages:   row.Messages,
			Status:     statusFromCodes(row.Answered == 1, row.MaxFinal, row.SawInvite == 1),
		})
	}

	return out, nil
}

type callRow struct {
	CorrKey   string `db:"corr_key"`
	AnyCallID string `db:"any_call_id"`
	AnyXCID   string `db:"any_x_cid"`
	FromUser  string `db:"from_user"`
	ToUser    string `db:"to_user"`
	StartedAt string `db:"started_at"`
	EndedAt   string `db:"ended_at"`
	Messages  int    `db:"messages"`
	Answered  int    `db:"answered"`
	MaxFinal  int    `db:"max_final"`
	SawInvite int    `db:"saw_invite"`
}

// GetCall returns every message of one call, oldest-first. id is a
// correlation key: if it matches any x_cid, the call is every call_id
// seen with that x_cid; otherwise id is a bare call_id.
func (r *Reader) GetCall(ctx context.Context, id string) ([]SipMessage, error) {
	callIDs, err := r.resolveCallIDs(ctx, id)
	if err != nil {
		return nil, err
	}

	if len(callIDs) == 0 {
		return nil, nil
	}

	query, args, err := sqlx.In(
		`SELECT data FROM sip_messages WHERE call_id IN (?) ORDER BY ts ASC, id ASC`, callIDs)
	if err != nil {
		return nil, fmt.Errorf("build get call query: %w", err)
	}

	query = r.db.Rebind(query)

	var datas []string

	if err := r.db.SelectContext(ctx, &datas, query, args...); err != nil {
		return nil, fmt.Errorf("get call: %w", err)
	}

	out := make([]SipMessage, 0, len(datas))

	for _, d := range datas {
		var rec jsonRecord

		if err := json.Unmarshal([]byte(d), &rec); err != nil {
			return nil, fmt.Errorf("decode record: %w", err)
		}

		out = append(out, rec.toMessage())
	}

	return out, nil
}

func (r *Reader) resolveCallIDs(ctx context.Context, id string) ([]string, error) {
	var ids []string

	if err := r.db.SelectContext(ctx, &ids,
		`SELECT DISTINCT call_id FROM sip_messages WHERE x_cid = $1`, id); err != nil {
		return nil, fmt.Errorf("resolve x_cid: %w", err)
	}

	if len(ids) == 0 {
		ids = []string{id}
	}

	return ids, nil
}

// Stats returns method/response counters bucketed by interval, including
// the Active (in-conversation) gauge.
func (r *Reader) Stats(ctx context.Context, from, until time.Time, interval time.Duration) ([]StatPoint, error) {
	if interval <= 0 {
		interval = time.Minute
	}

	secs := int64(interval.Seconds())
	if secs < 1 {
		secs = 1
	}

	// Bucket by flooring the UTC epoch of the (text) ts to the interval.
	// `ts::timestamp AT TIME ZONE 'UTC'` interprets the stored UTC string
	// as UTC regardless of session timezone, so EXTRACT(EPOCH) is exact.
	query := `
SELECT
  (FLOOR(EXTRACT(EPOCH FROM (ts::timestamp AT TIME ZONE 'UTC')) / $1) * $1)::bigint AS bucket,
  SUM(CASE WHEN method = 'INVITE' THEN 1 ELSE 0 END)                AS invites,
  SUM(CASE WHEN response_code BETWEEN 200 AND 299 THEN 1 ELSE 0 END) AS answered,
  SUM(CASE WHEN response_code >= 400 THEN 1 ELSE 0 END)             AS failed,
  SUM(CASE WHEN method = 'BYE' THEN 1 ELSE 0 END)                   AS byes,
  SUM(CASE WHEN response_code BETWEEN 200 AND 299
            AND cseq LIKE '%INVITE%' THEN 1 ELSE 0 END)             AS invite_answers,
  COUNT(*)                                                          AS total
FROM sip_messages
WHERE ts BETWEEN $2 AND $3
GROUP BY bucket
ORDER BY bucket ASC`

	var rows []statRow

	if err := r.db.SelectContext(ctx, &rows, query, secs, formatTS(from), formatTS(until)); err != nil {
		return nil, fmt.Errorf("stats: %w", err)
	}

	out := make([]StatPoint, 0, len(rows))

	active := 0

	for _, row := range rows {
		active += row.InviteAnswers - row.Byes
		if active < 0 {
			active = 0
		}

		out = append(out, StatPoint{
			Bucket:   time.Unix(row.Bucket, 0).UTC(),
			Invites:  row.Invites,
			Answered: row.Answered,
			Failed:   row.Failed,
			Byes:     row.Byes,
			Active:   active,
			Total:    row.Total,
		})
	}

	return out, nil
}

type statRow struct {
	Bucket        int64 `db:"bucket"`
	Invites       int   `db:"invites"`
	Answered      int   `db:"answered"`
	Failed        int   `db:"failed"`
	Byes          int   `db:"byes"`
	InviteAnswers int   `db:"invite_answers"`
	Total         int   `db:"total"`
}

// statusFromCodes derives a human call status.
func statusFromCodes(answered bool, maxFinal int, sawInvite bool) string {
	switch {
	case answered:
		return "answered"
	case maxFinal >= 400:
		return "failed"
	case sawInvite:
		return "ringing"
	default:
		return "other"
	}
}
