package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"
)

// This ledger is separate from the source diagnostic: completing a diagnostic
// does not grant a worker a perpetual capability to read its retained evidence.
type sandboxRun struct {
	ID            string         `json:"id"`
	Source        string         `json:"source_id"`
	Run           string         `json:"run_id"`
	Profile       string         `json:"profile"`
	Scope         []string       `json:"scope"`
	State         string         `json:"state"`
	Created       time.Time      `json:"created_at"`
	Updated       time.Time      `json:"updated_at"`
	Deadline      time.Time      `json:"deadline"`
	Lease         *time.Time     `json:"lease_until"`
	Generation    int            `json:"generation"`
	Calls         int            `json:"calls"`
	RequestBytes  int            `json:"request_bytes"`
	ReservedBytes int            `json:"reserved_bytes"`
	Note          string         `json:"note"`
	Result        *sandboxReport `json:"result"`
}

type sandboxReport struct {
	Mode        string                     `json:"mode"`
	LiveModel   bool                       `json:"live_model"`
	Digest      string                     `json:"evidence_sha256"`
	Claims      map[string][]evidencePoint `json:"observations"`
	Limitations []string                   `json:"limitations"`
}

var errBudget = errors.New("Sandbox replay budget exhausted")

func readSandbox(row interface{ Scan(...any) error }) (j sandboxRun, err error) {
	var b []byte
	if err = row.Scan(&b); err == nil {
		// Preserve uint64 counter values through PostgreSQL JSON, not float64.
		err = decodeExact(b, &j)
	}
	return
}

func (s *taskStore) getSandbox(ctx context.Context, id string) (sandboxRun, error) {
	return readSandbox(s.db.QueryRowContext(ctx, `SELECT to_jsonb(j) FROM sandbox_runs j WHERE id=$1`, id))
}

func (s *taskStore) submitSandbox(ctx context.Context, id, source, run, profile string) (j sandboxRun, err error) {
	err = s.change(ctx, func(tx *sql.Tx, now time.Time) error {
		var e error
		j, e = readSandbox(tx.QueryRowContext(ctx, `SELECT to_jsonb(j) FROM sandbox_runs j WHERE id=$1`, id))
		if e == nil {
			if j.Source != source || j.Profile != profile || j.Run != run {
				return errIDConflict
			}
			return nil
		}
		if !errors.Is(e, sql.ErrNoRows) {
			return e
		}
		sourceTask, e := readTask(tx.QueryRowContext(ctx, `SELECT to_jsonb(j) FROM investigations j WHERE id=$1`, source))
		if e != nil {
			return e
		}
		if sourceTask.Run != run {
			return errEvidenceUnavailable
		}
		if _, e = exportEvidence(sourceTask); e != nil {
			return e
		}
		scope := []string{sourceTask.Chain + "-sub-1"}
		switch profile {
		case "chain":
			scope = []string{sourceTask.Chain + "-publisher", sourceTask.Chain + "-relay", sourceTask.Chain + "-sub-1", sourceTask.Chain + "-sub-2"}
		case "subscriber-1":
		default:
			return errEvidenceUnavailable
		}
		var queued int
		if e = tx.QueryRowContext(ctx, `SELECT count(*) FROM sandbox_runs WHERE state='queued'`).Scan(&queued); e != nil {
			return e
		}
		if queued >= 20 {
			return errQueueFull
		}
		b, _ := json.Marshal(scope)
		j, e = readSandbox(tx.QueryRowContext(ctx, `INSERT INTO sandbox_runs AS j(id,source_id,run_id,profile,scope,state)
 VALUES($1,$2,$3,$4,$5,'queued') RETURNING to_jsonb(j)`, id, source, run, profile, b))
		return e
	})
	return
}

func (s *taskStore) cancelSandbox(ctx context.Context, id string) (j sandboxRun, err error) {
	err = s.change(ctx, func(tx *sql.Tx, now time.Time) error {
		_, e := tx.ExecContext(ctx, `UPDATE sandbox_runs SET state='cancelled', generation=generation+1,
 lease_until=NULL, updated_at=$2, note='Cancelled; admitted bytes cannot be recalled, no new reads or results accepted'
 WHERE id=$1 AND state IN ('queued','running')`, id, now)
		if e == nil {
			j, e = readSandbox(tx.QueryRowContext(ctx, `SELECT to_jsonb(j) FROM sandbox_runs j WHERE id=$1`, id))
		}
		return e
	})
	return
}

func (s *taskStore) claimSandbox(ctx context.Context, run string) (j sandboxRun, err error) {
	err = s.change(ctx, func(tx *sql.Tx, now time.Time) error {
		// Never retry a lost worker automatically. Reservations survive a crash;
		// an operator must explicitly admit another run with a new request ID.
		_, e := tx.ExecContext(ctx, `UPDATE sandbox_runs SET
 state=CASE WHEN deadline<=$2 THEN 'expired' ELSE 'blocked' END,
 generation=generation+1, lease_until=NULL, updated_at=$2,
 note='Deadline, plant run change or worker lease loss; no automatic replay'
 WHERE state IN ('queued','running') AND (run_id<>$1 OR deadline<=$2 OR lease_until<=$2)`, run, now)
		if e != nil {
			return e
		}
		var active int
		if e = tx.QueryRowContext(ctx, `SELECT count(*) FROM sandbox_runs WHERE state='running'`).Scan(&active); e != nil || active >= 2 {
			return e
		}
		j, e = readSandbox(tx.QueryRowContext(ctx, `UPDATE sandbox_runs j SET state='running',
 generation=generation+1, lease_until=$2::timestamptz + interval '15 seconds', updated_at=$2, note='Starting isolated replay'
 WHERE id=(SELECT id FROM sandbox_runs WHERE state='queued' AND run_id=$1 ORDER BY created_at,id LIMIT 1)
 RETURNING to_jsonb(j)`, run, now))
		if errors.Is(e, sql.ErrNoRows) {
			return nil // Commit maintenance even when there is no work to claim.
		}
		return e
	})
	return
}

func sandboxOwned(j sandboxRun, generation int, now time.Time) bool {
	return j.State == "running" && j.Generation == generation && j.Lease != nil && now.Before(*j.Lease) && now.Before(j.Deadline)
}

// A permit is committed before any bytes are sent to the worker. The fixed
// response reservation is never refunded, including on a broken pipe or crash.
// It is a byte/read budget, NOT a currency/token budget or a provider permit.
func (s *taskStore) sandboxPermit(ctx context.Context, id string, generation, requestBytes, reserveBytes int) error {
	return s.change(ctx, func(tx *sql.Tx, now time.Time) error {
		j, e := readSandbox(tx.QueryRowContext(ctx, `SELECT to_jsonb(j) FROM sandbox_runs j WHERE id=$1`, id))
		if e != nil {
			return e
		}
		if !sandboxOwned(j, generation, now) {
			return errFenced
		}
		if requestBytes < 0 || requestBytes > 16<<10 || reserveBytes < 0 || reserveBytes > 128<<10 ||
			j.Calls >= 12 || j.RequestBytes+requestBytes > 64<<10 || j.ReservedBytes+reserveBytes > 512<<10 {
			return errBudget
		}
		_, e = tx.ExecContext(ctx, `UPDATE sandbox_runs SET calls=calls+1,request_bytes=request_bytes+$2,
 reserved_bytes=reserved_bytes+$3,updated_at=$4 WHERE id=$1`, id, requestBytes, reserveBytes, now)
		return e
	})
}

func (s *taskStore) updateSandbox(ctx context.Context, j sandboxRun, state, note string, report *sandboxReport) error {
	return s.change(ctx, func(tx *sql.Tx, now time.Time) error {
		current, e := readSandbox(tx.QueryRowContext(ctx, `SELECT to_jsonb(j) FROM sandbox_runs j WHERE id=$1`, j.ID))
		if e != nil {
			return e
		}
		if !sandboxOwned(current, j.Generation, now) {
			return errFenced
		}
		var lease *time.Time
		if state == "running" {
			until := now.Add(15 * time.Second)
			lease = &until
		}
		b, e := json.Marshal(report)
		if e != nil {
			return e
		}
		_, e = tx.ExecContext(ctx, `UPDATE sandbox_runs SET state=$2,note=$3,result=$4,
 lease_until=$5,updated_at=$6 WHERE id=$1`, j.ID, state, note, b, lease, now)
		return e
	})
}
