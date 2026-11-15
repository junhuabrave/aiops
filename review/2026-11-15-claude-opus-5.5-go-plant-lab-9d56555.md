# Review: Go plant lab and durable investigations at 9d56555

**Date:** 15 November 2026. **Reviewer:** Claude Opus 5.5 (`claude-opus-5-5`), signed `[claude-opus-5.5]`. AI-assisted code review by reading only. **Input:** [PR #4](https://github.com/junhuabrave/aiops/pull/4) at `9d56555` (32 files, +4,831), checked against the merged lab spec on `main` (`40a32e3`). **Follows:** [lab spec evals and Temporal review at d2eacc4](2026-11-14-claude-opus-5.5-lab-evals-temporal-d2eacc4.md).

I did not run the code: this machine has no Go toolchain or Debian PostgreSQL 17. GitHub CI (`lab`, which runs install, race tests, rule checks and the smoke test) passed in 3m21s. This review is not owner approval.

## Verdict

**One small fix before merge; otherwise a solid first increment.** The PR stays inside the spec's boundaries. It makes no model calls, has no executor and no production connection. Fault controls are written only by the terminal harness. Telemetry ingestion never touches the task database. The README is unusually honest about what is unfinished. The lifecycle design (one advisory lock, generation fencing, one reclaim, run-change blocking, commit-ordered event IDs) is sound. The one real defect is in how the worker handles an ordinary database error (B1).

## What I checked

- **Task lifecycle (`tasks.go`, `task_http.go`):**
  - Admission is capped at 20 queued and 2 running.
  - A retry with the same request ID is idempotent; a retry with a different body gets 409.
  - Cancel bumps the generation, so a late `observe` call is fenced off.
  - A lost lease keeps the task's slot and is reclaimed once, and a second loss fails the task.
  - Tasks from an old plant run are blocked, not continued.
  - Because every write holds the global advisory lock, `bigserial` event IDs follow commit order, so an SSE cursor cannot skip an event.
- **Window verdict:** comparing `Conclusion` and `Summary` across the three samples is safe because every `Summary` is a fixed string, apart from the subscriber name. Changing evidence therefore yields `inconclusive`, as described.
- **Diagnostics (`console.go`):** no nil dereference on `byID[...-sub-1].Delivery`. Any missing, stale or rate-less node returns `visibility_limited` before that line runs.
- **Collector (`collector.go`):**
  - Each batch is validated as a whole.
  - Run, generation, group, interval and histogram shape are enforced.
  - Out-of-order or duplicate samples cannot overwrite newer ones.
  - A missing sample is reported as `missing`, never as zero traffic.
- **Plant (`plant.go`, `wire.go`):**
  - Queues are bounded at 1,000 frames and drop new frames when full.
  - The 32-frame acknowledgement window is enforced on both sides, and an acknowledgement with nothing outstanding drops the connection.
  - Frames carry a CRC and are tied to their run.
  - Sequence counting resets on a new generation.
  - The ledgers are independent of the collector.
- **Console security:** constant-time token comparison, `HttpOnly` + `SameSite=Strict` cookies, an Origin check and JSON content type on every non-GET request, CSP, and authorization re-checked during streams. Grafana runs as anonymous Viewer behind the console session with cookies stripped.
- **Spec conformance:** the lab's eight scenarios are six of the spec's eight cases plus `healthy` and `quiet-telemetry-failure`. The README states that misleading-change and the ambiguous pair are not implemented yet.

## Fix before merge

### B1. One transient database error gives up the worker's own task and spends its only reclaim (`tasks.go`, `work`)

```go
done, err = s.observe(call, owned.ID, owned.Generation, c.view)
...
if done || err != nil {
    owned = investigation{}
}
```

Any error from `observe` makes the worker drop its task, not just `errFenced`: a 3 s context timeout, PostgreSQL's 2 s `statement_timeout`, or a lost connection all count. The task stays `running` with a live 20 s lease, so no worker can pick it up. When the lease expires, `maintain` uses up the task's single reclaim. A second transient error during the recovered run then fails the task ("Worker lease lost twice"). Two short database stalls can therefore fail an investigation that never lost its worker. During the stall the abandoned task also holds one of the two running slots.

**Smallest fix:** drop ownership only when the task is finished or the worker has been fenced off. On any other error, keep `owned` and retry on the next tick. The generation fence already makes that retry safe. Then add a test that injects one failing `observe` call and checks that `reclaims` stays 0.

```go
if done || errors.Is(err, errFenced) || errors.Is(err, sql.ErrNoRows) {
    owned = investigation{}
}
```

## Non-blocking suggestions

1. **The session calendar is set by the fault command** (`applyScenario` → `sessions.json`). The diagnostic separates "quiet session" from "upstream interruption" only through `v.Sessions`. Reading a declared calendar is legitimate. But here the same harness command that injects the fault also writes the calendar, so the calendar always matches the case. Before the evaluation phase, make the calendar its own fixture that can be varied independently of the fault, so it can't act as a hidden case label. The spec lists this as a stop signal.
2. **Telemetry identity is asserted, not authenticated.** All eight processes and the host share one `collector.token`, so any of them can post records for any other identity. That's fine for a single-host lab. The PR body's "authenticated process/host telemetry with identity" should say "shared-token authenticated; identity validated against the manifest". Per-process tokens can come later.
3. **Rate across a telemetry gap.** `Rate` is computed from the current and previous samples whatever the gap between them. After telemetry comes back, the first rate is averaged over the whole outage. Return `nil` when `dt` is more than about 2× the interval, so a restored node needs two fresh samples before it shows a rate. This matters for E3's post-restoration fixtures.
4. **A new tab replays every event ever recorded.** A fresh tab connects with `after=0`, and the event table is never cleaned up. Each new tab therefore streams the whole event history in batches of 100 with no pause. Start new clients from the current maximum event ID; the board already loads current state through `GET /api/investigations`.
5. **`crypto.randomUUID()` only exists in a secure context** (`app.js:184`). Over plain HTTP to a non-loopback address, submitting an investigation fails. The orb portal (HTTPS) and SSH tunnels to `localhost` work. Either say so in the README or fall back to `crypto.getRandomValues`.
6. **PR wording:** "All eight live plant scenarios passed" reads as the spec's eight cases. Say "the lab's eight scenarios (six spec cases plus two controls)".

## Questions for the author

- Did the lifecycle tests cover a transient (non-fencing) `observe` error? B1 suggests they didn't.
- Is `quiet-telemetry-failure` meant to stand in for part of the ambiguous family, or is it an extra control?

— `[claude-opus-5.5]`, 15 November 2026
