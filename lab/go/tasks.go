package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/url"
	"path/filepath"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
)

// This is a bounded diagnostic queue, not a general workflow engine. Only its
// service touches PostgreSQL; no model or independently credentialed worker exists.
type investigation struct {
	ID         string      `json:"id"`
	Run        string      `json:"run_id"`
	Chain      string      `json:"chain"`
	Question   string      `json:"question"`
	State      string      `json:"state"`
	Created    time.Time   `json:"created_at"`
	Updated    time.Time   `json:"updated_at"`
	Deadline   time.Time   `json:"deadline"`
	Lease      *time.Time  `json:"lease_until"`
	Generation int         `json:"generation"`
	Reclaims   int         `json:"reclaims"`
	Samples    []diagnosis `json:"samples"`
	Result     *diagnosis  `json:"result"`
	Note       string      `json:"note"`
}

func (j investigation) active() bool { return j.State == "queued" || j.State == "running" }

type taskEvent struct {
	ID    int64     `json:"id"`
	Task  string    `json:"task_id"`
	State string    `json:"state"`
	Note  string    `json:"note"`
	At    time.Time `json:"at"`
}

type taskStore struct{ db *sql.DB }

var errQueueFull = errors.New("Investigation queue is full; retry later")
var errIDConflict = errors.New("Request ID already belongs to a different investigation")
var errFenced = errors.New("Investigation is no longer owned by this worker")

func databaseURL(dir string) string {
	u := url.URL{Scheme: "postgresql", User: url.User("plantlab"), Path: "/postgres"}
	q := url.Values{"host": {dir}, "port": {"15432"}, "sslmode": {"disable"}, "connect_timeout": {"2"}, "statement_timeout": {"2000"}}
	u.RawQuery = q.Encode()
	return u.String()
}

func openTasks(ctx context.Context, dsn string) (*taskStore, error) {
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(8)
	db.SetMaxIdleConns(4)
	s := &taskStore{db}
	// fsync and synchronous_commit retain their PostgreSQL defaults. This is local
	// demo durability, not replicated storage or survival of orb deletion.
	_, err = db.ExecContext(ctx, `
CREATE TABLE IF NOT EXISTS investigations (
 id text PRIMARY KEY, run_id text NOT NULL, chain text NOT NULL CHECK (chain IN ('a','b')),
 question text NOT NULL, state text NOT NULL CHECK (state IN ('queued','running','completed','blocked','failed','cancelled','expired')),
 created_at timestamptz NOT NULL DEFAULT clock_timestamp(), updated_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 deadline timestamptz NOT NULL DEFAULT clock_timestamp() + interval '5 minutes', lease_until timestamptz,
 generation integer NOT NULL DEFAULT 0, reclaims integer NOT NULL DEFAULT 0,
 samples jsonb NOT NULL DEFAULT '[]', result jsonb, note text NOT NULL DEFAULT 'Waiting for a diagnostic slot'
);
CREATE INDEX IF NOT EXISTS investigation_state ON investigations(state, created_at);
CREATE TABLE IF NOT EXISTS task_events (
 id bigserial PRIMARY KEY, task_id text NOT NULL REFERENCES investigations(id),
 state text NOT NULL, note text NOT NULL, at timestamptz NOT NULL DEFAULT clock_timestamp()
);`)
	if err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

// All lifecycle mutations take one transaction-scoped lock. This intentionally
// small queue trades throughput for auditable admission and commit-ordered event
// IDs across service instances. Telemetry ingestion never takes this lock.
func (s *taskStore) change(ctx context.Context, fn func(*sql.Tx, time.Time) error) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(17012026)`); err != nil {
		return err
	}
	var now time.Time
	if err = tx.QueryRowContext(ctx, `SELECT clock_timestamp()`).Scan(&now); err != nil {
		return err
	}
	if err = fn(tx, now); err != nil {
		return err
	}
	return tx.Commit()
}

func readTask(row interface{ Scan(...any) error }) (investigation, error) {
	var b []byte
	var j investigation
	if err := row.Scan(&b); err != nil {
		return j, err
	}
	err := json.Unmarshal(b, &j)
	return j, err
}

func saveTask(ctx context.Context, tx *sql.Tx, j *investigation, now time.Time) error {
	j.Updated = now
	samples, err := json.Marshal(j.Samples)
	if err != nil {
		return err
	}
	result, err := json.Marshal(j.Result)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `UPDATE investigations SET state=$2, updated_at=$3, lease_until=$4,
 generation=$5, reclaims=$6, samples=$7, result=$8, note=$9 WHERE id=$1`,
		j.ID, j.State, now, j.Lease, j.Generation, j.Reclaims, samples, result, j.Note)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO task_events(task_id,state,note) VALUES($1,$2,$3)`, j.ID, j.State, j.Note)
	return err
}

func (s *taskStore) submit(ctx context.Context, id, run, chain, question string) (j investigation, err error) {
	err = s.change(ctx, func(tx *sql.Tx, now time.Time) error {
		var e error
		j, e = readTask(tx.QueryRowContext(ctx, `SELECT to_jsonb(j) FROM investigations j WHERE id=$1`, id))
		if e == nil {
			if j.Run != run || j.Chain != chain || j.Question != question {
				return errIDConflict
			}
			return nil // Retries use the caller's stable request ID, even after completion.
		}
		if !errors.Is(e, sql.ErrNoRows) {
			return e
		}
		var queued int
		if e = tx.QueryRowContext(ctx, `SELECT count(*) FROM investigations WHERE state='queued'`).Scan(&queued); e != nil {
			return e
		}
		if queued >= 20 {
			return errQueueFull
		}
		j, e = readTask(tx.QueryRowContext(ctx, `INSERT INTO investigations AS j(id,run_id,chain,question,state)
 VALUES($1,$2,$3,$4,'queued') RETURNING to_jsonb(j)`, id, run, chain, question))
		if e != nil {
			return e
		}
		return saveTask(ctx, tx, &j, now)
	})
	return
}

func (s *taskStore) get(ctx context.Context, id string) (investigation, error) {
	return readTask(s.db.QueryRowContext(ctx, `SELECT to_jsonb(j) FROM investigations j WHERE id=$1`, id))
}

func (s *taskStore) list(ctx context.Context) ([]investigation, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT to_jsonb(j) FROM investigations j
 ORDER BY (state IN ('queued','running')) DESC, created_at DESC LIMIT 100`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	jobs := []investigation{}
	for rows.Next() {
		j, e := readTask(rows)
		if e != nil {
			return nil, e
		}
		// The board needs metadata, not every stored process observation.
		j.Samples = nil
		j.Result = nil
		jobs = append(jobs, j)
	}
	return jobs, rows.Err()
}

func (s *taskStore) cancel(ctx context.Context, id string) (j investigation, err error) {
	err = s.change(ctx, func(tx *sql.Tx, now time.Time) error {
		var e error
		j, e = readTask(tx.QueryRowContext(ctx, `SELECT to_jsonb(j) FROM investigations j WHERE id=$1`, id))
		if e != nil || !j.active() {
			return e
		}
		j.State, j.Note = "cancelled", "Cancelled by operator; no further snapshots or results will be accepted"
		j.Generation++
		j.Lease = nil
		return saveTask(ctx, tx, &j, now)
	})
	return
}

// Expired leases preserve collected evidence. One reclaim gets a new generation;
// a second loss fails the task instead of creating an unbounded retry loop.
func (s *taskStore) maintain(ctx context.Context, run string) error {
	return s.change(ctx, func(tx *sql.Tx, now time.Time) error {
		rows, err := tx.QueryContext(ctx, `SELECT to_jsonb(j) FROM investigations j WHERE state IN ('queued','running')
 AND (run_id<>$1 OR deadline<=$2 OR lease_until<=$2)`, run, now)
		if err != nil {
			return err
		}
		var jobs []investigation
		for rows.Next() {
			j, e := readTask(rows)
			if e != nil {
				rows.Close()
				return e
			}
			jobs = append(jobs, j)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
		for _, j := range jobs {
			switch {
			case j.Run != run:
				j.State, j.Note = "blocked", "Plant run changed; retained evidence is historical. Start a new investigation for this run."
			case !now.Before(j.Deadline):
				j.State, j.Note = "expired", "Five-minute investigation deadline reached"
			case j.Reclaims >= 1:
				j.State, j.Note = "failed", "Worker lease lost twice; partial evidence retained"
			default:
				// Keep the reserved active slot: a recovery must not overflow the
				// twenty queued incidents. claim() picks up unleased running work.
				j.Note = "Worker lease lost; one recovery attempt available"
				j.Reclaims++
			}
			j.Generation++
			j.Lease = nil
			if err = saveTask(ctx, tx, &j, now); err != nil {
				return err
			}
		}
		return nil
	})
}

func (s *taskStore) claim(ctx context.Context, run string) (j investigation, err error) {
	err = s.change(ctx, func(tx *sql.Tx, now time.Time) error {
		var e error
		j, e = readTask(tx.QueryRowContext(ctx, `SELECT to_jsonb(j) FROM investigations j
 WHERE run_id=$1 AND deadline>$2 AND state='running' AND lease_until IS NULL ORDER BY created_at LIMIT 1`, run, now))
		if errors.Is(e, sql.ErrNoRows) {
			var active int
			if e = tx.QueryRowContext(ctx, `SELECT count(*) FROM investigations WHERE state='running'`).Scan(&active); e != nil {
				return e
			}
			if active >= 2 {
				return sql.ErrNoRows
			}
			j, e = readTask(tx.QueryRowContext(ctx, `SELECT to_jsonb(j) FROM investigations j WHERE run_id=$1
 AND state='queued' AND deadline>$2 ORDER BY created_at, id LIMIT 1`, run, now))
		}
		if e != nil {
			return e
		}
		lease := now.Add(20 * time.Second)
		j.State, j.Note, j.Lease = "running", "Diagnostic worker acquired a slot; collecting three observations, five seconds apart", &lease
		j.Generation++
		return saveTask(ctx, tx, &j, now)
	})
	return
}

// The in-memory collector read is bounded and held inside the lifecycle transaction.
// Cancellation and lease fencing therefore guard both new reads and publication.
// Do not put network/model calls inside this transaction; those need explicit permits.
func (s *taskStore) observe(ctx context.Context, id string, generation int, view func() plantView) (done bool, err error) {
	err = s.change(ctx, func(tx *sql.Tx, now time.Time) error {
		j, e := readTask(tx.QueryRowContext(ctx, `SELECT to_jsonb(j) FROM investigations j WHERE id=$1`, id))
		if e != nil {
			return e
		}
		if j.State != "running" || j.Generation != generation || j.Lease == nil || !now.Before(*j.Lease) || !now.Before(j.Deadline) {
			return errFenced
		}
		lease := now.Add(20 * time.Second)
		j.Lease = &lease
		// Recovery must not count the same observation window twice.
		if len(j.Samples) != 0 && now.Sub(j.Samples[len(j.Samples)-1].At) < 5*time.Second {
			_, e = tx.ExecContext(ctx, `UPDATE investigations SET lease_until=$2 WHERE id=$1`, id, lease)
			return e
		}
		v := view()
		if v.Run != j.Run {
			return errFenced
		}
		j.Samples = append(j.Samples, diagnose(v, j.Chain))
		j.Note = fmt.Sprintf("Recorded observation %d/3; four process identities in chain %s", len(j.Samples), j.Chain)
		if len(j.Samples) == 3 {
			d := j.Samples[2]
			for _, earlier := range j.Samples[:2] {
				if earlier.Conclusion != d.Conclusion || earlier.Summary != d.Summary {
					d.Conclusion = "inconclusive"
					d.Summary = "Observations changed during this investigation. Inspect all three timestamped snapshots; no stable root cause is established."
					d.Next = "Compare fault onset or recovery against this window before proposing an action."
					break
				}
			}
			j.Result, j.State, j.Lease = &d, "completed", nil
			j.Note = "Three-observation diagnostic completed; this is not a declaration of delivery recovery"
			done = true
		}
		return saveTask(ctx, tx, &j, now)
	})
	return
}

func (s *taskStore) events(ctx context.Context, after int64) ([]taskEvent, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id,task_id,state,note,at FROM task_events WHERE id>$1 ORDER BY id LIMIT 100`, after)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	events := []taskEvent{}
	for rows.Next() {
		var e taskEvent
		if err = rows.Scan(&e.ID, &e.Task, &e.State, &e.Note, &e.At); err != nil {
			return nil, err
		}
		events = append(events, e)
	}
	return events, rows.Err()
}

func (s *taskStore) work(ctx context.Context, c *collector) {
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	var owned investigation
	var next time.Time
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		call, cancel := context.WithTimeout(ctx, 3*time.Second)
		err := s.maintain(call, c.manifest.Run)
		if err == nil && owned.ID == "" {
			owned, err = s.claim(call, c.manifest.Run)
			next = time.Time{}
		}
		if err == nil && owned.ID != "" && !time.Now().Before(next) {
			var done bool
			done, err = s.observe(call, owned.ID, owned.Generation, c.view)
			next = time.Time{}
			if err == nil {
				next = time.Now().Add(5 * time.Second)
			}
			// A failed write/commit is not a lost lease, even if observe already
			// calculated done. Retry next tick under the same generation fence.
			if (done && err == nil) || errors.Is(err, errFenced) || errors.Is(err, sql.ErrNoRows) {
				owned = investigation{}
			}
		}
		cancel()
		if err != nil && !errors.Is(err, sql.ErrNoRows) && !errors.Is(err, errFenced) && ctx.Err() == nil {
			log.Printf("investigation worker unavailable: %v", err)
		}
	}
}

func labDatabaseDir(runDir string) string {
	return filepath.Join(filepath.Dir(filepath.Dir(runDir)), ".database")
}
