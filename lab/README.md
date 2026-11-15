# Go plant lab — delivery and parallel investigations

This is a **functional, single-host synthetic preview**, not the completed AI demo or a fleet-throughput qualification. Eight real Go processes deliver real length-prefixed TCP frames; the web console reads real observations. It has no production connection, model calls, action executor or approval button.

## Run on a disposable Linux amd64 host

The tested native package baseline is Debian 13: Go 1.24.4, Prometheus 2.53.3, Alertmanager 0.28.1 and PostgreSQL 17.12. Debian packages follow the configured repository's security updates; exact monitoring versions are recorded per run, not silently treated as immutable pins. Grafana 12.3.0 is pinned and its archive checksum verified. The pgx driver is pinned in `go.mod`/`go.sum`. No Docker daemon is required.

From the repository root:

```sh
bash lab/install.sh
make -C lab up MODE=mock
```

`up` runs in the foreground and owns its children. Interrupt it to stop the stack, or run `make -C lab down` in another terminal. Each launch creates a new run and retains previous evidence. A child exiting terminates the stack rather than silently replacing its process generation. Only one lab instance can run on a host with the default fixed ports.

Open port 8080 on your own machine and sign in with the generated value in `lab/.run/current/operator.token`. Do not commit or share that file. Sessions are read-only, expire after 12 hours and disappear when the collector restarts. There is currently **one broad demo operator**, not production SSO or the specification's restricted viewer. Grafana is available through the console's authenticated link. All other HTTP and delivery listeners bind loopback.

Use HTTPS (including the orb portal) or a loopback URL such as `http://localhost:8080`, directly or through an SSH tunnel. Investigation submission uses `crypto.randomUUID()`, which requires a secure browser context; plain HTTP to a non-loopback address is unsupported.

In an Amp orb, `.agents/setup` installs dependencies and builds the binary; `amp orb services ensure` runs the same Make launcher under supervision and prints the registered portal URL. Use that URL, not the host's loopback address. Stop with `amp orb service stop plant-lab` to avoid supervisor restarts. Do not run a second launcher alongside it.

## Exercise the plant

In another terminal, choose a harness-only scenario:

```sh
make -C lab scenario CASE=slow-consumer CHAIN=a
make -C lab scenario CASE=healthy
```

Supported cases: `healthy`, `slow-consumer`, `relay-bottleneck`, `upstream-interruption`, `telemetry-failure`, `healthy-busy`, `quiet-session`, and `quiet-telemetry-failure`. Each command resets the other controls. They are terminal operations, not browser or model tools. Wait for fresh observations and queue drainage between cases. The unaffected chain provides a control. `quiet-telemetry-failure` checks that an inactive session does not hide lost telemetry.

These are six specification cases plus two controls (`healthy` and `quiet-telemetry-failure`), not the specification's complete eight-case evaluation suite. The quiet telemetry failure is an extra control, not a substitute for the ambiguous family. Misleading-change and the ambiguous pair remain unimplemented. The fault harness also sets the declared session calendar; evaluation must use an independently varied calendar fixture before results can support diagnostic-quality claims.

The investigation form currently runs a **deterministic baseline against the selected chain**. It does not interpret natural language or delegate to agents. Results include the exact source snapshots and distinguish a supported symptom pattern, inconclusive evidence and limited visibility. They cannot identify a synthetic cap from symptoms alone. Question text is displayed safely as text; it cannot authorize a tool or alter the diagnosis. There is no hidden scenario lookup.

## Run investigations in parallel

Start an investigation for chain A, then choose chain B and start another. The form becomes available as soon as admission is committed; it does not wait for diagnosis. The board shows **two active slots and twenty queued slots**, with explicit overload rather than silent loss. Select **View** to inspect a task or **Cancel** to stop further diagnostic work. Closing a tab or losing its connection does not cancel anything. The board displays all active tasks and the newest records up to 100; older records remain addressable by ID.

Each diagnostic collects three real collector snapshots at least five seconds apart, normally taking about ten seconds plus scheduling delay. All three observations are retained. Differing diagnostic conclusions or symptom descriptions produce an inconclusive window result rather than presenting the last snapshot as a stable RCA. Completion means the investigation finished, not that delivery recovered. These are two Go diagnostic worker loops, **not two LLM specialists**.

The Go service owns a private PostgreSQL database in `lab/.database/`, outside individual plant runs. It persists IDs, scope, questions, state, deadlines, evidence and ordered progress events. PostgreSQL uses a Unix socket in an owner-only directory, no TCP listener, and its normal synchronous durability settings. This survives process/service restarts on this filesystem, **not loss/deletion of the orb or host**. There is no replicated database or backup service.

Admission and lifecycle changes use short PostgreSQL transactions with one shared lock, including across service connections. This intentionally bounded lab queue is not a scheduler for 2,000 servers. Telemetry ingestion never writes to this database or takes that lock. Workers renew twenty-second leases around five-second observation steps; one lease recovery is allowed, increments the ownership generation, and retains existing evidence. Cancellation/expiry fences subsequent reads/results. Failed recovery retains partial evidence. No network or model calls run inside the lifecycle transaction.

Restarting the whole lab creates a new plant run: unfinished old investigations become **blocked**, not silently continued against replacement processes; completed results remain historical and labeled accordingly. Same-run lease recovery is exercised by the lifecycle tests, not a claim that this launcher independently restarts workers while keeping the plant running.

The preview API is session-authorized, with the same broad demo-operator scope on every read and stream:

| Route | Contract |
| --- | --- |
| `POST /api/investigations` | `{id, chain, question}`; 32-character lowercase hex request ID; 202 after commit. Reuse the same ID/body after an uncertain response. A conflicting body gets 409; a full queue gets 429 and `Retry-After: 5`. |
| `GET /api/investigations` | Active-first board metadata, latest 100 records. |
| `GET /api/investigations/{id}` | Durable state and the exact observation/result bundles. |
| `GET /api/investigations/{id}/evidence` | Frozen, versioned observation export with a SHA-256 checksum. Completed three-snapshot tasks only; 409 for unfinished tasks or older records without process generations. Same operator-session authorization as the task. |
| `POST /api/investigations/{id}/cancel` | Idempotent cancellation; already-terminal tasks retain their outcome. |
| `GET /api/events` | Committed progress events; resume with `Last-Event-ID` or `after`. Session validity is rechecked during streaming. Ten-second streams reconnect automatically; polling also refreshes board state. |
| `POST /api/investigate` | Earlier instantaneous snapshot diagnostic, retained for the plant smoke evaluator; not a durable investigation. |

There is one diagnostic task per investigation: no child-task delegation, regional scheduling, priority/fairness policy, automatic alert intake, backend-query reservations or model budgets yet. Those controls must be implemented before external tool/model workers are attached. For the global plant, regional telemetry and independently bounded investigation pools remain the target architecture—not one agent per process and not this single-host queue presented as fleet qualification.

## Exercise the first AI contracts without a provider

`lab/ai` runs an actual **LangGraph flow with scripted replay responses**, not a live LLM. It validates the proposed coordinator/specialist boundary against frozen observations exported from the Go service. It does not consume the telemetry stream, change a task's durable state, or write results back into the web console. The console remains the deterministic baseline.

With Python 3.13 and uv installed (tested uv 0.12.9), run from the repository root:

```sh
make -C lab verify-ai
# Requires the disposable lab already running on its default port:
make -C lab replay CHAIN=a
```

The operator-side capture script signs in locally using the existing token file without printing it, creates one normal diagnostic task, and exports its completed window. It then runs full-chain and subscriber-only replays. Evidence and JSON reports are saved under `lab/.run/current/replay-<task-id>/`. The replay itself receives only the export file: no token, database connection, fault controls, baseline diagnosis or scenario assignment. The SHA-256 verifies exact payload bytes; it is **not a signature or access credential**. Keep exported files private like the original evidence.

The flow admits one to three distinct roles (operations, integration, engineering), runs each specialist with a separate delivered-evidence context, permits one coordinator-selected follow-up, and selects validated observations for the report. Specialists run sequentially in this harness; these are not isolated worker pools or a parallel-performance demonstration. `--demo` deliberately scripts all three roles to exercise plumbing, not to demonstrate intelligent task selection. Custom transcripts can choose one useful specialist instead.

Contracts enforced in code:

- Seven fixed read-tool names with role allowlists; resource IDs must stay inside the offline scope. Shared-relay queue points require both relay and peer scope. Raw queries, URLs, shell commands, specialist delegation and operational actions have no accepted schema.
- Immutable parsed observations with run/task identity, process generation, peer, exact source/collection timestamps, units, counter/gauge type and explicit fresh/stale/missing states. Only three snapshots are present, not continuous history. Derived rates, host-wide evidence, independent session calendars and change records are unavailable; `recent_changes` returns unavailable, not an empty change history.
- Claims must match an actual point delivered to that specialist: resource, generation, peer, metric, timestamps, point statistic, value and unit. V1 supports exact point claims only—no inferred aggregations or unit conversions. Output prose is rendered from accepted fields. Unknown citations, incorrect numbers and extra causal/action prose block the replay; earlier validated observations remain labeled partial through `status: blocked`.
- At most 12 replay calls, 30 tool calls and five minutes per run. Aggregate request/response limits are 512/64 KiB, each response at most 16 KiB and each tool result at most 128 KiB. These are **local replay limits**, not tokens, currency reservations or cross-replica quotas. Cancellation/deadline checks reject late replay results. There is no persistent checkpoint or automatic retry.
- Reports show ordered progress events, evidence/prompt/runbook hashes, scope, validated claims and missing checks. All reports say `mode: replay`, `live_model: false`, `causality: not_evaluated`; completion proves neither diagnosis nor recovery. LangSmith tracing is explicitly disabled, including when tracing flags exist in the environment.

To replay a saved export with a JSON transcript instead of the scripted demonstration:

```sh
uv run --locked --project lab/ai python lab/ai/investigator.py /path/to/evidence.json --responses /path/to/responses.json
```

The transcript is an array of `{role, stage, response}` objects. Stages are `plan`, `investigate`, `follow_up`, `select`; the strict schemas and example transcripts are in `lab/ai/investigator.py` and its contract tests. Exit code 2 means invalid input or a blocked replay. Dependencies and hashes are pinned in `lab/ai/uv.lock`; this increment tested LangGraph 1.2.22 and Pydantic 2.14.3. LangSmith is a framework dependency, not an enabled external service.

**Before connecting a live model:** implement Go-enforced worker identities, generation-fenced read/model permits, durable shared budget reservations, restricted-viewer authorization, separate worker credentials/checkpoints and constrained egress. Select an available approved model and obtain an explicit spend ceiling. This harness does not implement those production-shaped boundaries; a role string or `--scope` option is not an authenticated identity. It also does not evaluate prompt-injection resistance or diagnosis quality—scripted rejection tests measure enforcement, not model behavior. Live single-agent versus multi-agent comparisons remain pending.

## Verify without model credentials

```sh
make -C lab verify
# On a host where no lab is already running:
python3 lab/tests/smoke.py
# Against an existing supervised disposable lab, including the full healthy interval:
python3 lab/tests/smoke.py --existing --warmup 60 --healthy-seconds 600
```

The smoke runner owns the harness during its run: do not inject faults manually at the same time. It checks independent publisher/subscriber sequence ledgers, healthy and busy controls, localized faults, fresh/stale semantics, alert journal timing and quiet-session behavior. It restores healthy controls on success and writes `lab/.run/current/verification.json`; on failure, inspect the retained state and restore it explicitly. A short CI smoke is **not** the 10-minute healthy gate. No model credentials or paid calls are used by tests or CI.

`verify` runs Go tests with the race detector, HTTP session/CSRF and input-boundary tests, diagnostic counterexamples, real-PostgreSQL lifecycle tests and Prometheus rule tests. Lifecycle tests create and remove their own disposable databases; they never use the running lab database. They exercise concurrent admission/claim limits across connections, request deduplication/conflicts, cancellation and late-read fencing, one-reclaim recovery, expiry, changed-run blocking, overlapping observation windows, persistence after reopening the service and SSE cursor replay. This is **not** the full specification's isolation or live-AI qualification command.

`make -C lab verify-ai` runs credential-free LangGraph contract tests. For plant scenarios plus Go-export/Python-replay integration, use `uv run --locked --project lab/ai python lab/tests/smoke.py --ai-replay` (add `--existing` only against an already running disposable lab). CI runs both suites and retains synthetic export/replay reports. The native plant can still run without installing the Python AI environment.

## Implemented boundaries and remaining work

- Go owns frames, independent 1,000-frame drop-new queues, application-consumption ACK windows capped at 32, source timestamps, checksums and sequence accounting. Defaults are 500 publications/s per chain and two subscribers per chain. Application frames are 256 bytes plus a 4-byte TCP framing prefix; this is not an AMPS-compatible protocol.
- Delivery telemetry refreshes every second; process and actual-host resources every five seconds. Ingestion uses one shared collector token and caps requests at 256 KiB and 100 records. Identity is validated against the manifest, not authenticated per process: a token holder can submit another registered process's identity. Unknown identities/generations/groups and invalid batches are rejected; duplicate/out-of-order samples cannot overwrite newer observations. Host metrics have a separate identity. Resource refresh does not mask stale delivery telemetry.
- The collector retains latest/previous snapshots, not a durable stream. Sequence gaps and unsent snapshots are counted. Prometheus scrapes separate fast/resource endpoints and retains 24 hours per run. Stale source values remain explicitly timestamped and are excluded from live rate displays and delivery rules.
- Prometheus and Alertmanager run independently of the console's diagnostics. One webhook receiver appends and syncs a local alert journal. The other deliberately targets an unavailable AI intake, exercising receiver independence without a model service. This journal is a single-host demo page sink, not a production HA paging service.
- Loopback binding, request limits and browser sessions are functional protections, **not enforced worker isolation**. These processes share a Unix account and have no cgroup resource limits. No AI worker may be attached with production credentials under this launch arrangement.
- The preview uses `/api/*` endpoints with PostgreSQL task durability and SSE/resume/cancellation. Frozen evidence exports, offline scoped-tool contracts, a bounded LangGraph replay and exact point-claim validation are implemented. Restricted-viewer API authorization, live worker orchestration/isolation, durable model egress budgets and live AI remain unfinished. Neither replay nor the rule baseline passes the model safety/usefulness evaluations by itself.
- Misleading-change/paired-ambiguity scenarios, adjustable metric-group schedules, restart/reset semantics within a run, full wire-byte accounting, retention cleanup, resource-limit enforcement and the 20k-identity benchmark are not yet implemented. The lab does not claim 20,000 real processes or production-scale throughput.

Run directories contain private launch tokens, fault controls, logs, alert receipts, sequence ledgers, Prometheus/Grafana data and reports. They and the task database are ignored by Git. **Ledgers, investigation history and progress events grow without retention cleanup**; this launcher is for supervised development runs, not an unattended always-on installation. Stop it after a demonstration. Cleanup is deliberately manual in this increment so it cannot delete another run's evidence.

Review follow-ups remain before evaluation or longer-lived use: the first displayed rate after a telemetry gap currently averages over that gap, and a fresh browser tab replays the full event history from cursor zero. Gap-aware rate suppression and a race-safe current-state/event-cursor bootstrap are not implemented in this increment.

The engineer onboarding guide is deferred at the owner's request. Keep changes to this increment separate from existing untracked `plantops/` experiments.

## Executed local checks — 14 November 2026

On this Linux orb (4 visible CPUs, 7.8 GiB RAM), `make -C lab verify` and `go vet ./...` passed. A 60-second warmup followed by a 600-second healthy run reconciled 601,700 publications against both subscribers (1,203,400 receipts, including the final pause/drain boundary). No new gaps, duplicates, out-of-order frames or corruption appeared during that interval.

All eight functional scenarios passed. First observed threshold breach to synced alert-journal receipt was 7.399s for slow consumption, 7.345s for relay delay, 7.289s for upstream interruption and 8.704s for telemetry loss. The AI receiver was unavailable throughout. Healthy siblings and the other chain kept receiving; busy and quiet controls produced no delivery alerts; quiet mode did not suppress telemetry-loss alerts.

After the source-state display and host-identity corrections, the eight-case short smoke passed again (delivery-fault journal times 7.391–7.510s; telemetry loss 6.343s). Final browser checks covered login, healthy state, slow consumption, stale evidence, connection failure/recovery, literal rendering of HTML-like input, resource evidence, Grafana's six populated panels and a 390px Chromium layout with no horizontal overflow. The inspected recording compares healthy Chain B with affected Chain A and expands the evidence. These are deterministic-baseline results, **not live-AI evaluation or production readiness**. GitHub CI is configured but has not run for these local changes.

## Parallel-investigation checks — 15 November 2026

The Go race suite passed, including real-PostgreSQL tests with concurrent admission and worker claims from two service connections. Lifecycle tests also passed when invoked as root, as the CI container runs. Two warm `.agents/setup` runs completed successfully in under one second each.

The eight-case live plant smoke passed again after the task service was attached (30-second healthy interval, not a repeated ten-minute qualification). Fault-to-journal timing remained 7.265–7.356s for delivery faults and 6.706s for telemetry loss. A separate live parallel probe confirmed a slow-consumption finding on A and healthy observations on B, with no cross-chain evidence. Losing telemetry between snapshots yielded `supported`, then two `visibility_limited` observations and an `inconclusive` window result. A full supervised-stack restart preserved a completed evidence bundle unchanged and blocked the unfinished old-run task.

Browser checks exercised two running investigations plus a queued third, cancellation, reload/reconnection, retained completed observations, unavailable-board warnings and recovery, and a 390px Chromium layout without horizontal overflow. The inspected recording shows real pointer movement across adjacent controls, starting A and B, cancelling B and expanding A's completed evidence. These checks do not establish multi-region failover, worker isolation, live-AI quality or 20k-process capacity. All implementation changes are local until separately published for review.

## Offline AI-contract checks — 15 November 2026

`make -C lab verify verify-ai`, `go vet ./...` from `lab/go`, and Python syntax/unused-name checks passed. The Go tests include session-protected exports, stable payload checksums, missing-versus-zero observations, independent resource freshness and refusal of absent/changed process generations. Nine Python test methods cover exact claims (including integers above 2⁵³ and nanosecond timestamps), role/scope violations, raw queries, recursive delegation, fan-out/follow-up limits, budgets, cancellation, late replies, and disabled network tracing. These are contract tests with scripted proposals, not model evaluations.

The rebuilt lab passed all eight existing functional smoke scenarios with a 30-second healthy interval. Delivery-fault threshold-to-journal times were 7.516–7.612 seconds; telemetry loss was 6.472 seconds. Both chains exported completed tasks that passed full-chain and subscriber-only replays. Each scripted demonstration used nine replay calls, three tool reads and zero provider calls. Separate live slow-consumer and telemetry-failure captures preserved the delayed/healthy peer distinction and stale relay evidence; restricted subscriber reports did not inherit the relay's out-of-scope freshness warning. The lab was restored to healthy controls afterward. No UI was changed, no live model ran, and no fleet-throughput or diagnostic-quality claim follows from these results.
