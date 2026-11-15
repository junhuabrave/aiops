# Review: bounded AI orchestration replay and evidence contracts at 5d3d1c6

**Date:** 15 November 2026. **Reviewer:** Claude Opus 5.5 (`claude-opus-5-5`), signed `[claude-opus-5.5]`. AI-assisted code review by reading only. **Input:** [PR #5](https://github.com/junhuabrave/aiops/pull/5) at `5d3d1c6` (15 files, +2,013 / −17), checked against `main` at `6ceb2d1` and the merged lab spec. **Follows:** [Go plant lab re-review at f0c45da](2026-11-15-claude-opus-5.5-go-plant-lab-rereview-f0c45da.md).

I did not run the code locally. GitHub CI (`lab`, now including `verify-ai` and `smoke.py --ai-replay`) passed in 4m18s. This review is not owner approval.

## Verdict

**Confirmed; no blockers.** This is a clean first AI-contract increment. It makes no provider calls, and LangGraph sits above the Go/PostgreSQL lifecycle rather than replacing it, which matches the merged architecture. E1's core rule is enforced in code: a claim must match evidence actually delivered to that specialist, field for field, and the report text is rendered from validated fields. The tests are adversarial rather than happy-path. The four non-blocking points below are mostly about edges of "exact" and "redacted".

## What I checked

- **Frozen export (`evidence.go`, `task_http.go`):**
  - Export is allowed only for a completed task with three increasing samples, four chain resources, and process generations that stay the same across samples.
  - Missing data is exported as `null` with `missing` availability, never as zero. Stale values keep their timestamp and are marked `stale`.
  - Derived rates and baseline conclusions are excluded, which correctly sidesteps the rate-gap issue I raised on PR #4.
  - Point IDs are deterministic.
  - The checksum covers the exact payload string, so Python doesn't need to reproduce Go's JSON formatting. It is described honestly as damage detection, not proof of origin.
  - A task from before this PR (no `generation` field) gets 409, not a partial export.
- **Window validation (`investigator.py`, `Window`):** the harness doesn't trust the export's own labels. It rechecks topology, point provenance, units and metric kinds, queue-peer rules, missing-vs-zero, and freshness.
- **Claims:**
  - Matched exactly against the point *delivered to this specialist*, including generation, peer and the nanosecond-precision timestamp string. A citation the specialist didn't receive is rejected.
  - A claim on stale or missing evidence is rejected.
  - Bools, strings and NaN are rejected as values.
  - A rejected turn adds no claims, because acceptance is all-or-nothing per turn.
- **Roles and budgets:**
  - Fixed per-role tool sets, no raw queries, no delegation from specialists.
  - At most three distinct specialists and one follow-up.
  - Selection may only cite known claim IDs, once each, and extra prose fields fail the schema.
  - Model calls, tool reads, request/response bytes and time are capped. A tool result over the limit fails rather than being silently truncated.
  - Leftover replay entries cause a failure.
- **Tracing:** `tracing_context(enabled=False)` is applied, and a test sets ambient LangSmith environment variables and checks that nothing is sent.
- **CI and dependencies:** `uv.lock` with `--locked`. Replay runs against live exports for both chains, in full and subscriber-only scope.

## Non-blocking suggestions

1. **Restricted scope still leaks a peer's throughput through relay totals** (`tool`, peer filter). With scope `{a-relay, a-sub-1}`, the queue points toward `a-sub-2` are correctly withheld. But `delivery_window` still returns the relay's total `egress`, which counts frames sent to both subscribers. Subtract `a-sub-1`'s `ingress` and you get roughly how much `a-sub-2` received. The ambiguous-case review on PR #3 hit the same kind of leak through combined CPU/network totals. Pick one: document relay totals as shared-process evidence visible to anyone with relay scope, or withhold relay `ingress`/`egress` unless every peer is in scope. Then add a test for whichever you choose.
2. **Render facts from the cited evidence, not from the claim the model echoed** (`run`, facts). `claim != wanted` compares pydantic models with Python's numeric equality, so a claim of `23.0` matches a point of `23`, and the rendered text then says `23.0 messages`. A strict type check would be wrong too, because Go writes a float64 `0` as `0`, which Python reads as an int. Since the claim must already equal the point, look up the delivered point and render its value and unit. The model's own spelling then never reaches the operator.
3. **A blocked run still lists facts the coordinator never selected** (`run`). On `blocked`, `facts` falls back to `list(self.claims)`, so a run that failed at `select` (for example, by citing an invented ID) still shows every validated claim as a fact. Those claims already appear in `validated_claims`. Leave `facts` empty when blocked, so the only facts a reader sees are ones a completed run selected.
4. **Freshness is checked at microsecond precision against Go's nanoseconds** (`Window.consistency`). Python parses the timestamps to microsecond precision and compares `age <= 3`, while Go compared `age > 3s` in nanoseconds. A point that is stale by less than 1 µs could be judged fresh in Python, which raises "freshness mismatch" and rejects the whole valid export. It fails safe and is very rare, but it adds noise in large evaluation runs. Compute the age in integer nanoseconds from the timestamp strings, or allow 1 µs either way at the boundary.

## Agreed boundaries before a live model

The PR states these and I agree they gate live attachment. Cancellation (`cancelled`) and the budgets exist only inside one Python process. Before a provider is attached, cancellation should come from the Go task's generation fence, and spending should be reserved durably, so a restarted or duplicate worker can't reset the limits. The scope option and role names are not authenticated identities or OS isolation.

## Questions for the author

- Is relay-only scope meant to hide the relay's own queues? Today the queue points are dropped unless their peer is also in scope, so `{a-relay}` alone sees no queue evidence.
- E1 says derived calculations will be allowlisted. Will the first one, a gap-aware rate, be computed in the Go export or as a validated derived claim in Python?

— `[claude-opus-5.5]`, 15 November 2026
