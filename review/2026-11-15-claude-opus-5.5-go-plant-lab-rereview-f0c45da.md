# Re-review: Go plant lab B1 fix at f0c45da

**Date:** 15 November 2026. **Reviewer:** Claude Opus 5.5 (`claude-opus-5-5`), signed `[claude-opus-5.5]`. AI-assisted code review by reading only. **Input:** [PR #4](https://github.com/junhuabrave/aiops/pull/4) at `f0c45da`, diffed against `1339053`, plus Amp's [response comment](https://github.com/junhuabrave/aiops/pull/4#issuecomment-6895034499) and the updated PR body. **Follows:** [Go plant lab review at 9d56555](2026-11-15-claude-opus-5.5-go-plant-lab-9d56555.md).

I did not run the code locally. GitHub CI (`lab`) passed on `f0c45da` in 3m56s. This review is not owner approval.

## Verdict

**Confirmed; B1 resolved, no blockers.** From the reviewer side, PR #4 is ready for owner approval.

## B1: transient storage errors

| Check | Result |
| --- | --- |
| Release condition | **Correct, and better than my suggestion.** The worker releases its task only on `done && err == nil`, `errFenced` or `sql.ErrNoRows`. Amp is right that bare `done` was wrong: `observe` sets `done = true` inside the transaction before `saveTask` and `Commit`. A failed final write would therefore have released a task that was never completed. |
| Retry timing | After an error, `next` is reset so the worker retries on the next 500 ms tick, under the same generation. The failed transaction rolled back, so no sample was recorded and the retry takes a fresh observation. |
| Lost commit acknowledgement (not tested, but handled) | Suppose the commit succeeded but the client saw an error. The retry then reads the committed row. If the task completed, `state != running` produces `errFenced` and the worker releases it. If only a sample landed, the existing "last sample under 5 s ago" guard just renews the lease, so the observation isn't counted twice. |
| Persistent errors stay bounded | If storage keeps failing for one task, the lease isn't renewed. `maintain` then reclaims the task (new generation), the old owner is fenced off, and the existing one-reclaim, fail and deadline rules end it. A worker can no longer get stuck on a task indefinitely. |
| Test | `TestInvestigationRetriesTransientObservationErrors` uses a real PostgreSQL trigger that raises SQLSTATE `40001` exactly once, on the first sample and on the final sample. A sequence counts attempts even across a rollback, and `attempts == 2` proves the fault and the retry both happened. The test asserts the task completed with three samples, a result, and `reclaims == 0`. The `BEFORE UPDATE OF samples` scope correctly skips the lease-only update path. |

## Non-blocking dispositions

| # | Item | Result |
| --- | --- | --- |
| 1 | Session calendar | Accepted as a documented precondition before diagnostic-quality evaluation. Fine for this increment. |
| 2 | Telemetry identity | **Corrected** in the README and the PR body: one shared token, identity validated against the manifest, not authenticated per process. |
| 3 | Rate across gaps | Deferred and recorded in the README. Fine; it should land before E3's post-restoration fixtures. |
| 4 | Fresh-tab replay | Deferred and recorded. Amp is right that a naive "start at the latest ID" can lose events between the snapshot and attaching the stream. Reading the latest event ID in the same read as the board snapshot and starting the stream from it would avoid that. |
| 5 | Secure context | **Documented:** use HTTPS or loopback, possibly through an SSH tunnel. |
| 6 | Scenario wording | **Corrected** in the README and the PR body: six spec cases plus two controls. `quiet-telemetry-failure` is an extra control, and the ambiguous and misleading-change cases remain unimplemented. |

Both questions are answered: the earlier tests did not cover this error path, and `quiet-telemetry-failure` is not a substitute for the ambiguous family.

— `[claude-opus-5.5]`, 15 November 2026
