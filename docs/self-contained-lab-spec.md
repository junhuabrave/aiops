# Self-contained market-data investigation lab

Build a working synthetic plant and a read-only AI investigation demo before asking plant engineers for time. Demonstrate delivery, independent detection, bounded specialist delegation and evidence-backed conclusions; qualify telemetry throughput separately.

**Status:** proposed implementation contract, 14 November 2026, awaiting Claude review and owner approval. No implementation, benchmark, model evaluation or deployment is delivered by this document. All rates, thresholds, topology and resource budgets below are proposed synthetic defaults, not production facts or measured results.

This follows the [architecture](ai-layer-architecture.md) and [stack reference](production-stack-design.md). It specifies Stage 1's lab plus a limited Stage 2 read-only investigation slice so the owner can see an end-to-end demonstration. It does not complete all Stage 2 work: generated-skill development/promotion is deferred. No engineer input is a prerequisite; actual production adapters and acceptance thresholds remain future engineer-reviewed work.

## 1. The demo must answer an operational question, not narrate a script

The operator sees healthy delivery, then a symptom alert, and asks: “Why is delivery stale for these subscribers?” The system shows which specialist is working, which observations support each hypothesis, what contradicts it, and what to check next. It proposes a reviewed diagnostic/recovery runbook without executing it.

An operator-controlled test harness introduces and later removes the fault. Neither the coordinator nor a specialist receives the scenario name, injection command, seed, expected diagnosis or answer key. A demo with canned model output is labeled a replay and cannot satisfy the live-AI gate.

**Out of scope:** production credentials or traffic; an AMPS-compatible implementation; HA/replay/failover correctness; automated changes or approvals; Temporal; arbitrary shell/SSH/PromQL; generated runtime scripts; private GPU inference; Slack/Teams integration; multi-site availability claims. No new Mimir/Kafka/Loki/vector-database deployment is required.

## 2. Start with eight real delivery processes

Use two independent chains, each with one publisher, one relay and two subscribers: eight OS processes total. The unaffected chain is a control. Each publisher emits 500 messages/second by default, with 256-byte application frames including a versioned header. That is 1,000 published messages/second and 2,000 subscriber deliveries/second before loss; transport bytes must be measured separately.

Each frame carries `run_id`, `feed_id`, publisher generation, monotonically increasing sequence, source timestamp and checksum. The lab uses length-prefixed TCP frames, no filtering/conflation, no replay and no hidden retransmission. Subscribers count contiguous progress, missing sequences, duplicates, corruption and out-of-order application frames by feed/generation. A publisher restart creates a new generation rather than implying loss across the old counter boundary. TCP transport ordering is not a claim about the proprietary bus.

Relays maintain independent bounded subscriber queues. Default queue capacity is 1,000 frames per subscriber; when full, drop the new frame for that subscriber and increment an explicit counter. A slow subscriber must not block the other subscriber or the publisher. Socket writes have deadlines and bounded buffering. Each subscriber acknowledges application consumption solely for flow control, with at most 32 unacknowledged frames; this prevents kernel buffering from hiding a slow consumer and does not enable replay. Subscribers report receive freshness even when no new frame arrives. The harness records every connection/reconnect so gap accounting distinguishes stream interruptions from generation resets.

The fault harness can cap a relay's dispatch rate or a subscriber's drain rate. These are reproducible synthetic bottlenecks, not proof of CPU saturation on real software. Actual CPU/memory measurements remain separate from configured rate caps. Stop upstream publication to simulate an interruption; keep the publisher process alive and reporting stats to distinguish it from process death.

All message-path processes initially run on one lab host so elapsed-time comparisons use a shared clock. Inter-arrival age uses local monotonic time. Future multi-host trials must measure clock uncertainty before asserting one-way latency. Logical site/host labels are inventory fixtures, not physical failure domains.

## 3. Concrete components and trust boundaries

| Component | First implementation | Responsibilities and isolation |
| --- | --- | --- |
| Delivery plant | One Go module with publisher, relay and subscriber modes | Eight independent processes; bounded queues; metrics never block delivery |
| Telemetry collector | Go service | Receives bounded stats batches, retains latest per-process snapshots, exports Prometheus metrics; no Python decoding/routing of continuous telemetry |
| Observability | Prometheus, Alertmanager, Grafana | Separate fast/slow scrape jobs, deterministic rules, dashboards and independent alert receiver; no model dependency |
| Control API and task service | Go HTTP service plus PostgreSQL | Operator authentication, scoped tools, task leases, dispatch, evidence, durable progress and budgets; API/read tools may share a binary |
| Model workers | Python, LangGraph, Pydantic and official provider SDK | Separate coordinator/operations/integration/engineering identities; role-specific context and tool allowlists, no administrative credentials |
| Operator page | Go-served HTML with small browser JavaScript and SSE | Status, evidence, contradictions, proposals, cancellation and reconnect; Grafana holds time-series dashboards |
| Harness and evaluator | Separate Go harness plus Python offline scoring/tests | Fault controls, independent delivery accounting, hidden expected outcomes and evaluation reports; inaccessible from worker identities |

Use PostgreSQL for task state and modest, bounded evidence bundles; Prometheus stores time series. No per-observation PostgreSQL writes. Use a distinct checkpoint database role/schema for each worker capability and service-owned evidence tables. Workers cannot query another role's checkpoints, the task-service tables or the evaluator's records directly. Operator/API access, not a PostgreSQL credential, mediates shared evidence.

Package services as version-pinned containers where supported. Workers get no repository/harness mount, container socket, host namespace, privileged capability or unrestricted network route. They can reach only the scoped tool/task API, their checkpoint store and the approved model endpoint through constrained egress. Plant metrics, Prometheus, inventory/change fixtures and fault controls are private to their owning services. The collector has separate CPU/memory limits from delivery processes. The test controller is not an agent tool or a button exposed on the agent page.

A single-machine development launch may test functionality, but processes under one unrestricted Unix account do not qualify isolation. Before claiming the isolation gate, exercise denied access in enforced container/network boundaries or separate isolated environments. If an orb cannot enforce those boundaries, report the limitation and use an appropriate disposable Linux environment; do not weaken the gate. Multiple orbs are optional for later partition/worker-loss experiments, not a prerequisite and not a production failure-domain claim.

Start the functional demo on a measured 4-vCPU/8-GiB-class host if available, with explicit service limits and two concurrent model calls. This is a starting allocation, not a capacity guarantee. Record actual hardware, versions and limits. In Amp, use supervised services (`.amp/services.yaml` and `amp orb services ensure`) for launch and portal access; the implementation must arrange container/process supervision beneath that entry point. No production deployment is implied.

## 4. Telemetry has explicit identity, freshness and loss semantics

Plant processes send asynchronous, bounded batches over a private HTTP endpoint to the Go collector. A record contains `schema_version`, `run_id`, `process_id`, `generation`, `host_id`, `metric_group`, `sample_seq`, `observed_at`, `interval_ms`, and typed metric values with units. Sequences and freshness are tracked per process generation and metric group so a fresh fast-group update cannot mask stale resource data. IDs and metric names must match registered inventory/schema; cap body size at 256 KiB and 100 records per request. Collection may fail without slowing delivery; retain a bounded latest snapshot and count unsent/coalesced records.

The schema distinguishes process and host records with `resource_kind`. Host records omit `process_id`, use `host_id` and boot generation as identity, and have independent group sequences. Host metrics must not be attributed to every resident process. Benchmark records use the same union schema with explicitly synthetic identities.

The collector rejects malformed or unknown records, ignores duplicate or older sample sequences within a generation, and reports resets, gaps, queue age, rejected records and last source-observation time. A new generation cannot silently reuse the previous counter baseline. `202` acknowledges in-memory acceptance only, not durable storage. Collector restart may lose buffered snapshots; detect that loss and expose stale/missing evidence rather than promise exactly-once ingestion.

| Metric group | Source refresh / scrape / freshness limit | Required fields |
| --- | --- | --- |
| Delivery and queues | 1 s / 1 s / 3 s | Ingress/egress counters, per-subscriber drops, sequence anomalies, queue depth and interval maximum, oldest queued age, last receive age |
| Process resources | 5 s / 5 s / 15 s | CPU seconds, resident bytes, file descriptors, process generation and heartbeat |
| Host resources | 5 s / 5 s / 15 s | Actual host CPU/memory/network observations; distinguish these from synthetic logical-host fixtures |
| Inventory and changes | Versioned events, not a scrape cadence | Process/feed/subscriber edges, topology version, config version and scoped change records |

Expose separate fast/slow endpoints so scraping fast metrics does not refresh every resource metric. Cache source snapshots; label source freshness separately from scrape time. A latest-value cache does not preserve every source sample: counters and interval maxima preserve specified facts, instantaneous gauges can lose intervening values. Detect sequence gaps in telemetry independently from market-data sequence gaps. A test-only rate change affects an allowlisted group and its source/export schedule; agents can recommend a cadence but cannot change it.

Use bounded labels (`run_id` for the current run, logical process, generation, feed, subscriber and site). Clear old run state and enforce retention; never label by message sequence, timestamp, arbitrary client input or fault name. Keep 24-hour functional-lab metrics retention and delete only the selected disposable run on reset. The harness retains test evidence separately before cleanup.

## 5. Detection and paging continue without AI

Prometheus evaluates fast rules every second. The first lab rules are deliberately simple and versioned:

- With an active-session fixture, subscriber receive age above 3 seconds or oldest queue age above 2 seconds for 5 seconds raises a delivery symptom.
- Source-observation age above the metric-group freshness limit for 5 seconds raises a visibility alert. Missing expected processes are detected by joining an inventory-backed expected-target signal, not by comparing a missing gauge to zero.
- Expected quiet-session periods suppress only traffic-silence alerts; telemetry-loss alerts remain enabled. Sampled recovery never proves that a root cause is known.

Alertmanager sends to two isolated receivers: a durable local alert journal representing the existing page path, and AI intake. Failure or timeout of AI intake must not block the alert journal. Initial grouping delay is 1 second; group/update/retry parameters are pinned in the run manifest. Match the AI incident key to run, rule, affected resources and alert episode; repeated notifications update one episode, while a later resolved-and-reopened episode creates new work. Never deduplicate across caller/client scope.

Measure detector creation, alert-journal receipt, intake acceptance and first evidence separately. The functional target is journal receipt within 15 seconds of a sustained threshold breach, including persistence and scrape/evaluation delays. This is a synthetic gate, not a promised production SLO. Verify it with workers and the model endpoint disabled.

## 6. A small API defines replaceable plant adapters

The implementation adds a versioned OpenAPI document and JSON schemas. Use strict enums, finite numeric ranges, UTC timestamps, immutable evidence IDs and typed errors. Reject unknown fields where they can change authority. These are lab contracts to validate against production later, not claims that the proprietary plant already implements them.

| Interface | Minimum request and result contract |
| --- | --- |
| `POST /v1/incidents` | Authenticated caller, idempotency key, requested resources, question or validated alert reference; return durable incident ID and accepted scope after request/outbox commit |
| `GET /v1/incidents/{id}` | Authorized current viewer receives task states, deadline, budget usage, results and scoped evidence references |
| `GET /v1/incidents/{id}/events` | SSE with durable event IDs; resume by cursor, or send explicit snapshot/reset when cursor has expired; disconnect is not cancellation |
| `POST /v1/incidents/{id}/cancel` | Authorized, idempotent durable cancellation of the task tree; no new children, retries or tool permits afterward |
| `POST /v1/tools/{name}` | Worker-bound task ID/generation, resource IDs and bounded observation window; server derives entitlements and returns an evidence envelope or typed denial |
| `GET /v1/evidence/{id}` | Reauthorize viewer and stored resource/client scope, including referenced/derived results; an ID is not a credential |

Internal lease/heartbeat/result endpoints authenticate worker capability, bind task generation and perform atomic conditional transitions. They are not model tools. Use the task service as the sole lifecycle authority; LangGraph stores reasoning checkpoints within a task, not an independent scheduler.

Every evidence envelope includes `evidence_id`, resource/client scope, topology/config generation, requested and observed time ranges, source and collection timestamps, units, query-template version, completeness/truncation flags and a content checksum. Empty, zero, unavailable and stale are distinct results. A result contains observations with evidence references, hypotheses with supporting/contradicting references, missing checks, a conclusion (`supported`, `inconclusive`, or `visibility_limited`) and an optional runbook proposal. Do not expose private chain-of-thought or invented numerical confidence.

Start with these tools only: `topology_slice`, `delivery_window`, `queue_window`, `resource_window`, `recent_changes`, `telemetry_health`, and `runbook_lookup`. The service selects fixed backend queries; the model supplies typed IDs/windows, never raw PromQL, SQL, URLs or credentials. Evidence windows are limited to 10 minutes, 20 process IDs, 20,000 samples and 128 KiB per call, with a 5-second backend timeout. Truncation must be reported; no silent “all clear” from partial results.

Use two synthetic operator identities: a demo operator permitted to view both chains and a restricted viewer permitted to view one subscriber, plus service identities bound to roles. Generate credentials at launch and keep them out of Git/model context. API session validation, CSRF protection for browser mutations, output escaping, and authorization apply to progress, evidence and synthesized answers. Hosted portal authentication is additional protection, not a substitute for application authorization. Integration and engineering cannot retrieve another caller's data through a shared incident or cached result. Shared-relay evidence must be scoped/redacted without hiding that the view is incomplete.

## 7. Workers delegate within durable limits

The coordinator uses a constrained LangGraph flow: validate intent/scope, propose specialist tasks, await admitted results, request at most one discriminating follow-up, then synthesize or escalate. The Go service validates its plan. Allow at most three specialist tasks plus one follow-up per incident, depth one, with no specialist-to-specialist delegation. A simple case may use only operations; the UI must not invent parallel work to make the demo look busy.

| Role | Allowed investigation | Tools |
| --- | --- | --- |
| Coordinator | Plan and synthesize within caller scope | `topology_slice`, `runbook_lookup`, scoped task results |
| Operations | Compare source/relay/subscriber delivery and controls | `topology_slice`, `delivery_window`, `queue_window`, `telemetry_health`, `runbook_lookup` |
| Integration | Check versioned change timing, affected scope and counterexamples | `topology_slice`, `recent_changes`, `delivery_window`, `runbook_lookup` |
| Engineering | Distinguish queue/capacity limits from telemetry insufficiency; propose bounded next measurements | `topology_slice`, `resource_window`, `queue_window`, `telemetry_health`, `runbook_lookup` |

All runbooks are fixed, versioned diagnostic procedures shared across cases; they contain no scenario-to-answer lookup. Logs/change text/runbooks are untrusted content. Tool and task authorization is enforced outside the model; invalid or invented evidence references cannot become accepted findings. Any proposed restart/failover/tuning is text/typed proposal only, labeled “not executed; outside this demo's authority.” There is no action executor or approval button.

Default incident limits: 5 minutes, 12 model calls including retries, 30 tool calls, 120,000 input tokens and 12,000 total generated/reasoning tokens where reported, plus a configured currency ceiling. Missing usage accounting must reserve conservatively or block further requests, not imply free work. Use a 60-second model-call timeout; retries consume the same task budget. A limit is a hard stop with a partial evidence-backed result, not an invitation to create a new incident.

Admit at most two active incidents, two simultaneous model calls and four simultaneous backend queries; queue at most 20 distinct incidents. Retain alerts in the independent journal when intake is full; return explicit overload with bounded retry hints. Persist admission/budget reservations across service replicas and restarts, so worker count cannot multiply quotas. At expiry/cancellation, stop new work immediately and request bounded stopping of running calls; already-sent provider calls can still finish or be charged, but late results cannot revive the task.

Task states are `queued`, `running`, `completed`, `blocked`, `failed`, `cancelled`, `expired`. Incident summary is derived from durable tasks and distinguishes investigation completion from delivery recovery. Workers heartbeat every 5 seconds on a 20-second lease; reclaim increments generation atomically, with at most one reclaim per task. Reject stale-generation reads/results at the tool/task boundary. Retry delivery using the same ID; crash recovery resumes the checkpoint or reports failure without minting a replacement incident. Evidence already collected stays accessible only to authorized viewers.

The operator page shows alert time, observed client impact, active/completed tasks, last update, deadline, budgets, evidence links, disagreements and next required action. No fabricated percent-complete. After manual harness recovery, deterministic monitoring can show delivery recovered while the investigation remains inconclusive. SSE reconnect, narrow-scope access and cancellation must be exercised in the browser.

## 8. Use one real model and compare delegation with simpler baselines

Start with the architecture's Claude Opus 5.5 (`claude-opus-5-5`) candidate for all role identities, subject to actual account/region availability and synthetic-data eligibility. Do not provision another provider or GPU to build the first demo. Implementation must verify the returned deployment/model ID, tool support and usage accounting and pin the SDK/configuration. If unavailable, report the blocker and propose a supported substitute; no silent fallback or claim that a mock is live AI.

The task service owns limits; a single controlled model-egress boundary holds provider credentials and enforces allowed model, endpoint, request/response size and call reservations. Workers receive task-scoped access, not the provider key. This boundary may be a module of the control service, not another platform product. Keep traces local and sanitized; no LangSmith export is required. Provider spend requires an explicit owner-approved run ceiling before live evaluations; this specification authorizes neither API spending nor real-data export.

Provide three evaluation modes over identical frozen evidence windows and tool contracts: deterministic diagnostic rules, a single nondelegating investigator, and coordinator plus bounded specialists. For a fair comparison, the single-investigator evaluation identity can use the union of the same seven read tools under the same caller scope and total incident budgets; deterministic rules receive the same available evidence, not the oracle. This evaluation identity does not expand a specialist's normal role permissions. A replay/mock mode exists only for repeatable infrastructure tests. Record model, prompts, tool/runbook digests, input evidence hashes, randomization seed held by evaluator, tokens, provider errors, cost, elapsed time and software revision. Compare useful diagnoses and contradictions, not writing style or public benchmark scores.

## 9. Faults have independent evidence and negative controls

Generate private scenario assignments after worker images are built. Keep assignments, seeds and expected answers in an evaluator-only volume/service; do not expose them in resource names, labels, alerts, logs or worker mounts. Public fixtures describe possible behavior, never the selected test's truth. The scenario controller is the only fault writer; its receipts are scored alongside observed effects, not treated as proof that an injection succeeded.

| Case | Controlled perturbation | Evidence needed; plausible wrong answer to reject |
| --- | --- | --- |
| Relay bottleneck | Cap both subscriber dispatch queues on one relay at 250 messages/s each against 500/s input; keep ingress draining | Fresh upstream traffic, both downstream clients affected, growing relay queue/age, healthy other chain; do not label it upstream loss |
| Slow consumer | Cap one subscriber at 100 messages/s against 500/s offered | One client's queue/age/drop growth, healthy sibling and other chain; do not recommend whole-relay failover |
| Upstream interruption | Pause one publisher's message output, retain its telemetry | Source egress stops before downstream silence, queues drain, process heartbeat stays fresh; do not claim relay capacity saturation |
| Telemetry failure | Block collector updates for one process while delivery continues | Observation timestamps age while independent subscribers continue; report visibility limitation, not zero traffic or confirmed process death |
| Healthy busy period | Double both publishers to 1,000/s within configured relay/subscriber headroom | Continued sequence/freshness and bounded queues; do not call traffic growth an incident solely because volume doubled |
| Quiet session | Pause traffic during declared inactive session | No traffic-silence page, heartbeat/freshness checks still active; do not suppress a concurrent collector failure |
| Misleading change | Add a benign scoped change on the unaffected chain during a real fault | Compare changed targets and controls; timing alone cannot establish causality |
| Ambiguous evidence | Pair upstream interruption and stopped relay dispatch cases with identical visible downstream staleness; mask source delivery and relay queue/egress evidence in both | State missing discriminating evidence, preserve alternatives; validate that no other allowed tool reveals which case ran, and do not assert the hidden answer |

Harness-only faults must be removable and resettable by run ID. Verify induced effects before starting scored investigation; failed fault setup makes a run invalid, never an AI success. Freeze a common post-fault evidence window for comparisons while continuing to show live delivery separately. Randomize IDs, affected chain, onset and intensity independently; prevent run-name leakage. Do not claim an unseen-fault-family benchmark when only seeds changed.

Separate publisher and subscriber ledgers track generated and received sequence ranges/checksums respectively; the evaluator reconciles them after stopping publication and draining in-flight traffic. They are not populated from the agent's answer or the collector's expected counts. Score factual scope, cited values/units/windows, counterevidence, required abstention and proposals against evaluator predicates. The model that writes the answer cannot be its sole judge. Synthetic predicates establish only this lab's expected behavior; engineering adjudication is still required for proprietary plant RCA.

## 10. Gates separate a convincing demo from fleet throughput

| Gate | Executed check and required result |
| --- | --- |
| Healthy delivery | 60-second warmup then 10 minutes at baseline; no unexplained gaps, duplicates, corruption or out-of-order frames; control subscribers remain healthy during localized faults |
| Independent detection | Each sustained delivery fault reaches the alert journal within 15 seconds of threshold breach; healthy/quiet controls do not produce delivery pages; telemetry loss produces visibility alerts even with AI disabled |
| Tool/evidence safety | Cross-scope reads, stale generation, invented evidence IDs, unauthorized tool names, prompt injection, direct backend/harness access and revoked viewer sessions are denied in every finite safety case; no model-reachable mutation route exists |
| Lifecycle | Kill/restart workers and task service, resend alerts, exhaust budgets, cancel during tool/model work and reconnect SSE; one durable incident per episode, bounded retries, no accepted late result after cancellation/expiry, no widened scope |
| Investigation usefulness | Eight cases above, five private parameter sets each, three modes: 120 investigations. On at least 36/40 cases, the multi-role mode must satisfy the case-specific evidence/conclusion rubric; all telemetry-failure/ambiguous cases must avoid unsupported certainty. Report per-case results, not only aggregate accuracy |
| Delegation value | Report single-agent versus multi-role accuracy, counterevidence coverage, p50/p95 elapsed time, tokens and cost under equal incident budgets. If specialists add no useful evidence or accuracy, keep the simpler mode as default; orchestration support alone is not proof of benefit |
| Live demonstration | At least one real-model fault investigation and one negative/ambiguous control, plus an inspected recording showing alert, tasks, cited evidence and final result. Mock/replay runs do not satisfy this gate |

The 36/40 threshold is a proposed synthetic milestone, not statistical proof of general RCA quality. The ambiguous family's five scored assignments include both hidden alternatives; pair-equivalence validation is separate setup, not additional scored trials. Publish failed cases and full denominators; changes made after inspecting held-out results require a newly generated private set. Do not repeatedly tune on the same “holdout.” Repeat stochastic investigations in later qualification; the first small suite does not establish reliability bounds. Run live evaluations only after estimating and approving the total spend ceiling; a smaller demonstration does not count as the full evaluation suite.

**Separate telemetry benchmark:** a Go generator represents 20,000 logical process identities, each sending one update/second, plus 2,000 logical host identities at configurable 5-second or 1-second cadence. This is not 20,000 OS processes or market-data throughput. Keep benchmark signals separate from functional-demo metrics and disable model investigations during telemetry sizing.

Begin with ten scalar series per process/host update: at one-second host cadence, 22,000 updates/second carry 220,000 scalar observations/second. A 100-series process profile carries 2,020,000 observations/second including the ten-series host updates. Measure serialized bytes rather than assuming a 1-KiB payload. Report active series, label churn, sample rate, queue age, memory/GC, accepted/rejected/coalesced records and query freshness for each profile. Successful decoding is not successful storage/querying.

Use the same versioned record format and Go decoder/collector as the functional lab. Run both staggered and synchronized one-second bursts for 10 minutes after warmup, then collector restart and a 30-second metrics-backend outage. Scrape snapshots can intentionally coalesce updates; quantify source observations versus exported/retained samples and explicitly report missing gauge history. If later requirements demand every source observation, add a separately reviewed durable ingestion path; never claim this snapshot design provides that guarantee.

For the first ten-series steady-state profile, require bounded queues/RSS under declared limits, zero unaccounted loss, and observation-to-query p99 at most 5 seconds for retained fresh values. In outage trials, deliberate shedding/coalescing is allowed only with counters and explicit stale visibility; after recovery, fresh values must become queryable within 30 seconds. Compute percentiles from independent probe timestamps/sequence markers in the query response, not ingestion acknowledgements. Keep samples across missing intervals in the loss denominator. Identify how batching, scrape cadence and source refresh affect these numbers.

The 100-series profile is a measured capacity exploration, not an assumption that one lab host passes. Mark any unrun or failed profile clearly. Size the benchmark environment independently; exceeding a resource ceiling fails that run rather than silently changing workload or thresholds. Publish AI-off/on delivery comparisons on identical offered functional load and report latency/sequence differences; set no production impact claim from a co-located synthetic trial. Benchmark completion and the functional demonstration are separate review decisions.

## 11. Proposed repository and operator interface

These paths and commands are an implementation contract, not runnable tools in this documentation PR. Existing untracked experiments are not approved components and must not be overwritten or represented as validated work.

| Proposed path | Contents |
| --- | --- |
| `lab/go/` | Go plant, collector, control API and harness commands, sharing only versioned wire schemas where appropriate |
| `lab/agents/` | Python role workflows, scoped SDK/tool clients, pinned dependencies and deterministic mock adapter |
| `lab/contracts/` | OpenAPI/JSON schemas, metric catalog and versioned resource/evidence definitions |
| `lab/config/` | Prometheus/Alertmanager/Grafana provisioning, non-secret inventory/session fixtures and runtime profiles |
| `lab/runbooks/` | Reviewed read-only procedures with immutable release digests |
| `lab/tests/` | Lifecycle/authorization/browser tests, public scenario definitions and evaluator code; private generated assignments live outside worker mounts |
| `lab/Makefile` | Launch, health, scenario, validation, evaluation, benchmark and cleanup entry points |

| Proposed command | Required behavior |
| --- | --- |
| `make -C lab up MODE=mock` | Start supervised local services; print readiness and safe operator/Grafana access instructions; never fetch secrets from tracked files |
| `make -C lab up MODE=live` | Same stack, require approved credentials/model/spend cap; fail explicitly if unavailable |
| `make -C lab scenario CASE=slow-consumer` | Harness-only fault for the selected run; terminal/controller knows the case, worker/UI incident context does not |
| `make -C lab verify` | Deterministic contract, isolation and lifecycle checks; do not invoke paid models |
| `make -C lab evaluate MODE=live` | Preflight and enforce approved total spend, create private test set, run three modes and save complete results |
| `make -C lab benchmark PROFILE=process-20k-ten-series` | Independent declared-resource telemetry run; no implicit fleet simulation or throughput claim |
| `make -C lab reset RUN_ID=...` | Stop faults and remove only that disposable run after retaining its report; refuse unrelated targets |
| `make -C lab down` | Stop lab services without deleting retained evidence or unrelated work |

In an orb, share only registered portal URLs, never raw sandbox/localhost links. Persist reproducible launch/dependency setup in repository files during implementation. Provide a credential-free mock smoke test in GitHub CI; provider-dependent evaluations remain explicit budgeted runs, not automatic PR jobs. The current repository has no CI, so adding and exercising that smoke workflow belongs to implementation, not a claim made by this PR.

## 12. Deliver in reviewable increments

1. **Specification:** Amp opens this documentation PR; Claude reviews contracts, hidden-oracle separation, test discriminators and scope. Owner approves merge. No implementation or deployment is authorized by the document alone.
2. **Plant and observability:** implement real delivery, collector, deterministic alerts, dashboard, fault harness and CLI; verify healthy/fault/control runs without AI.
3. **Read-only investigation:** implement task service, scoped tools, one investigator, then coordinator/specialist pools and factual progress. Pass mock safety/lifecycle tests before requesting a budgeted live demo.
4. **Demonstration and evaluation:** run live fault and negative-control cases, record the UI and evidence, then run the approved comparative suite. Keep benchmark runs separately labeled and sized.
5. **Engineer-ready handoff:** provide one launch procedure, a short recorded/live demo, evidence-linked diagnoses, complete pass/fail results, known limitations and a list of synthetic assumptions to validate. Only then ask engineers how their actual telemetry, semantics and runbooks differ.

Amp owns implementation/integration and executed evidence; Claude reviews scoped PRs and results; the owner approves merges and any spend/environment provisioning. No third development agent is needed initially. If later delegated, the plant/telemetry boundary is a separable work package only after schemas are agreed. Agent consensus is not production authorization.

The earliest stop signals are failure to isolate runtime workers from oracle data, dependence on a hidden scenario label to diagnose faults, missing independent delivery verification, or inability to bound telemetry/query load. Fix those before adding more agents or presenting the POC as an operational system.
