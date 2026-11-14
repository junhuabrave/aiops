# Production stack and AI model design

**Recommendation:** keep the real-time messaging path unchanged; build a separately provisioned operations control plane using existing Prometheus/Grafana, Go plant adapters and Python/LangGraph investigators. Start with a PostgreSQL-backed task service for the read-only POC; adopt Temporal-managed task/action workflows in the action phase if incumbent automation does not meet the contracts. Use a qualified frontier model for investigation, with cheaper models introduced only where evaluations justify them.

**Status:** component-level design proposal, 14 November 2026. Extends the [architecture proposal](ai-layer-architecture.md). This extension received a separate Claude review; its agreed dispositions and pending corrective-revision review are tracked in the [review record](architecture-reviews.md). The earlier document's two reviewer rounds do not cover this extension. Nothing here is deployed, load-tested against the plant, or authorization for production changes. Fleet size is known; message rate, payload size, telemetry volume, site topology, and latency objectives are not.

## 1. Real-time delivery and AI have different workloads

The system must handle three independently sized workloads. A model never consumes the full quote stream, and a workflow engine never receives every metric sample.

| Plane | Workload and implementation | Timing and failure boundary |
| --- | --- | --- |
| Market-data delivery and fast protection | Existing proprietary bus and supported native controllers; use its existing SDK/language for any unavoidable native integration | Preserve current latency and correctness SLOs. No LLM, Temporal, or remote observability dependency on the message path |
| Telemetry and deterministic detection | Existing exporters/stats interfaces, sharded Prometheus, recording/alert rules, existing paging; Go adapters only for gaps | Metric-specific cadence and measured end-to-end detection delay. A required subsecond protection loop must use supported local/native checks, not ordinary scrape-and-LLM investigation |
| Investigation and controlled change | Authenticated API, durable workflows, bounded role workers, evidence services, approved executor | Asynchronous, seconds-to-minutes investigations and potentially longer approvals. These are workload categories, not promised latency bounds |

An incident arriving during market open does not permit an agent to increase scrape rate, run fleet-wide queries, or start extra workers without limits. Provision this layer away from latency-sensitive delivery cores, memory, disks, and network queues. Measure p99/p99.9 delivery latency and sequence correctness with the AI layer enabled and disabled at identical offered load.

The previous [client/pool diagram](client-agent-pools.png) still applies. Specialist pools scale with admitted investigation demand, not server count or messages per second.

## 2. Concrete reference stack, with adoption gates

This is the preferred implementation if no incumbent service already satisfies the stated contract. Versions must be supported, tested together, pinned in lockfiles/container digests, and promoted through staging; this document does not prescribe untested package-version combinations.

| Component | Selected stack | Deployment and contract |
| --- | --- | --- |
| Messaging plant | Existing software, supervisor, HA and native SDK | Unchanged. Do not substitute Kafka, Python, or an agent framework for market-data delivery |
| Process/host collection | Existing exporters and process stats; Go collector for missing mappings; native SDK bridge if required | Bounded collector per host or supported service group, not an LLM per process. Prefer cached/asynchronous stats snapshots over expensive work inside scrape handlers |
| Metrics ingestion | Existing Prometheus, sharded by site/target ownership, with explicit HA pairs where required | Configure discovery from inventory; keep local rules available during WAN failure. Verify duplicate-replica handling before remote write |
| Metrics retention/query expansion | Keep incumbent backend; Grafana Mimir is the preferred expansion candidate if measurements justify it | Mimir's preferred ingest-storage architecture introduces Kafka-compatible storage and object storage. That is a separately operated telemetry backend, not an agent task bus [1] |
| Logs and events | Existing log platform first; Loki if a new indexed log service is needed | Index bounded labels such as site/service; process IDs can be structured fields where label churn is excessive. Preserve process searchability. No quote-by-quote logging |
| Optional collection/routing | OpenTelemetry Collector | Add only for missing OTLP/routing needs. Separate pipelines when signals need different scaling. Bounded queues, memory limits, persistent buffering where justified [2] |
| Detection and paging | Existing alerting; Prometheus rules/Alertmanager if that is the incumbent | Deterministic thresholds, session-aware baselines, grouping/inhibition. AI receives an asynchronous incident copy; paging does not wait |
| Event intake, operator API, progress | Go HTTP service; existing ingress and OIDC SSO; REST/JSON and Server-Sent Events (SSE) | Persist receipt before acknowledging accepted work. Rate limits, idempotency, authorization, durable event cursor. Existing Slack/Teams adapter can be another client |
| Durable orchestration | Read-only POC: PostgreSQL-backed task service. Action phase: Temporal service plus Go workflow workers, if adopted | Phase-specific authority is defined in Section 4. Reuse incumbent workflow if it meets replay, cancellation, approval, versioning, and reconciliation contracts. Cloud versus self-hosted follows data/security policy [3] |
| Agent execution | Python, LangGraph, Pydantic, official provider SDKs; selected LangChain integrations | Three role-specific deployment pools, plus coordinator capability. Structured proposals and evidence; no production administrator credentials |
| Plant read tools | Go authenticated services; REST/JSON with OpenAPI for the first integration | Named query/check templates with typed parameters. gRPC/Protobuf only if a measured internal streaming interface needs it; MCP can expose the same tools later, not replace authorization |
| Plant action executor | Separate Go service using supported plant APIs or existing approved automation jobs | Action ledger, exact target binding, policy enforcement, execution fencing, outcome reconciliation. Existing Ansible Automation Platform is an option, not required |
| Policy | Existing authorization service; OPA if a new policy engine is required | Go services enforce policy decisions. Separate reviewed policy releases; stale/revoked authority blocks dispatch |
| Task index, topology, evidence metadata | PostgreSQL, using incumbent managed/HA service where available | Owns task lifecycle in the read-only POC. After Temporal adoption, retains intake/outbox, metadata, action ledger and UI projections, not a competing task scheduler. Never the metrics database |
| Large evidence and audit export | Existing private S3-compatible object storage | Encrypted, access-controlled bundles with checksums and retention. Durable write receipts and append-only audit controls; object links do not grant unrestricted access |
| Runbooks and skills | Git, CI, immutable signed release artifacts/container digests | Human-owned promotion and revocation. Separate sandbox for candidate code; scripts run only through approved execution controls |
| Model access | Existing enterprise model gateway if available; otherwise official SDK access behind one controlled egress boundary | Explicit provider/model allowlist, request budgets, data classification, usage accounting. Do not build a generic multi-provider platform before it is needed |
| Agent traces and evaluations | LangSmith if approved; otherwise internal traces plus pytest-based fixture/evaluation jobs | Keep plant evidence internal by default. Redact model traces before export. Deterministic checks score safety; engineers adjudicate diagnosis |
| Operator presentation | Existing incident console/bot first; React + TypeScript if a dedicated web console is needed | Durable status, evidence, approvals and cancellation. Grafana remains the telemetry dashboard; avoid rebuilding it |
| Deployment and secrets | Existing VM/container platform, enterprise secrets manager, workload identity, mTLS | Dedicated service identities and network policies. Kubernetes/Helm only if already operated or justified; do not migrate the plant for this project |

**Minimal first production-shaped lab:** Prometheus/Grafana, PostgreSQL, the Go API/read-tool boundary, Python role workers, approved model API, and synthetic publishers/relays/subscribers. Add Temporal and the separate executor when testing actions. Do not start with Mimir, Kafka, Loki, a vector database, service mesh, or GPUs merely because they appear in this table.

Keep Go/native continuous ingestion from the first lab to respect the owner's workload constraint and avoid qualifying a Python-only toy as the ingestion architecture. Reduce POC overhead by combining compatible Go API/read-tool responsibilities into one service where practical; preserve collector resource isolation and keep the privileged executor separate when introduced. Python remains for bounded investigations and offline analysis. This choice is not a claim of measured throughput.

## 3. Collect process evidence without disturbing delivery

### No Python in the continuous telemetry path

The design must accommodate the owner's stated case of 20,000 processes sending updates every second, plus server statistics when needed. This is a sizing requirement, not a measured payload or throughput result. Python/LangGraph consumes bounded incident evidence through read tools; it does not subscribe to, decode, route or persist the full telemetry stream.

Reuse the current stats transport and collectors where they already meet requirements. For missing streaming ingestion, use site-partitioned Go consumers with the supported bus SDK; prefer C++ where the plant only provides a mature native SDK or measured latency constraints require it. Language choice alone does not establish capacity. Keep any Python forecasting work off the continuous ingestion path, using bounded historical datasets or prepared features.

Partition by stable process identity within site, preserve source timestamps/generation/sequence where available, and bound decoding workers, batch sizes, flush intervals and queues. Handle duplicates, gaps, out-of-order updates, reconnects and replay explicitly. Use the existing wire format; adopt Protobuf for a new internal hop only if justified. Do not translate every update into a workflow, database transaction, HTTP model request or Python task.

If streamed stats are exposed through scrape endpoints, a latest-value cache does not preserve every one-second observation when scraped more slowly. Preserve required counters/distributions, or send the required timestamped observations through a supported batched ingestion path. Report intentional coalescing and loss of detail; never imply that downsampled data retains the original resolution. A slow telemetry subscriber must not backpressure market-data delivery: verify the bus's isolation behavior and use a separate collection path if necessary.

**Illustrative envelope:** 20,000 process updates/second × 1 KiB/update = 20.48 MB/second of payload. If all 2,000 servers also send a 1 KiB update every second, the total is 22.528 MB/second before protocol overhead, replication or replay. Payload sizes and server cadence here are assumptions, not recommendations. If every process update contains 100 scalar observations, process ingestion is instead described as 2 million observations/second; bytes, updates and samples are distinct capacity dimensions.

Qualify the entire decode/batch/store/query path with representative payloads, synchronized one-second bursts, maximum permitted server-stat collection, reconnect replay, a failed consumer shard and backend slowdown. Measure sustained throughput, observation-to-query p99 latency, CPU/memory/GC, queue age, loss and plant delivery impact. Do not approve a node count or claim Go/C++ capacity before this benchmark.

### Collection contract

Identify a process by logical process ID, host/site, and generation/start identity. Retain PID as an attribute, not the only identifier. Join lifecycle/topology/config metadata by version; a restarted process must not inherit the previous instance's counter baseline.

For each metric, register units, type, dimensions, reset behavior, source refresh interval, export/scrape interval, freshness limit, and allowed tuning bounds. Prometheus sample time alone does not prove a cached source refreshed; expose source-observation age or an equivalent supported signal.

For adjustable per-metric cadence, first establish what the current software supports. Prometheus scrape intervals normally apply to scrape jobs/targets, not individual series within one response. Different metric groups may need separate supported endpoints/jobs, or source/exporter controls. Do not scrape the same endpoint faster and claim only one metric became more expensive.

Keep counters and compatible histogram distributions for message volume, errors, queue age and latency. Avoid the process × symbol × subscriber cross-product in metrics. Store detailed event records in the existing log/event system, and use bounded drill-down while preserving individual process identity. Sampled tracing is optional; per-message traces are not a prerequisite for RCA.

### Scale and overload behavior

- Divide target ownership by site and shard. HA replicas may duplicate collection intentionally; the receiving backend must deduplicate correctly. Do not casually add independent scrapers that multiply workload or create competing writers.
- Keep essential local detection and independent delivery probes operational without central AI or WAN connectivity. Missing targets are reconciled against expected inventory, not interpreted as zero traffic.
- Set memory, CPU, disk and egress limits for collectors. Exporter queues and WALs have finite capacity; expose dropped/refused records, oldest buffered age and source freshness. Neither OTel buffering nor remote write guarantees zero loss [2].
- A full queue triggers explicit telemetry-loss handling. Never let telemetry retries block delivery threads. Define which diagnostic signals can be shed; keep that policy outside agent control.
- A future Mimir deployment separates ingest and query paths, but asynchronous ingestion can return stale query data after accepted writes. Check evidence timestamps/ingestion lag; use supported consistency controls where necessary and reject stale action preconditions [1].

### Evidence query admission

The read-tool service, not the model, selects credentials and backends. Begin with approved query templates: process health, ingress/egress comparison, slow-consumer checks, change history, redundancy readiness, and topology neighbours. Engineers can promote new templates through the skill lifecycle.

Apply per-site/backend/incident concurrency, time-range, series/sample, result-byte, timeout and wall-time limits. A truncated result explicitly reports incompleteness. Reserve query capacity for operators and alert evaluation; isolate agent queries through backend scheduling or an agent query tier where supported. Recording rules precompute reusable aggregates, while targeted raw histories remain available.

If arbitrary PromQL is later allowed, parse and validate its AST, enforce resource scope on every selector and expensive operation, and enforce upstream limits. Appending a label filter to a string is not a security boundary. Reject unbounded joins/regex/time ranges. Scope caches by entitlements, query version, time window and data freshness; cache hits never bypass read authorization.

## 4. Orchestration ownership and worker pools

### Read-only POC: task service, no Temporal dependency

The PostgreSQL-backed task service owns intake, task lifecycle, leases, deadlines, cancellation, budgets and dispatch. Persist the accepted request and dispatch intent atomically; a dispatcher claims work with a lease/generation and retries delivery using the same task ID. Duplicate delivery retrieves or resumes existing work, and stale generations cannot publish accepted results or continue tool calls. LangGraph persists reasoning checkpoints within that task; a checkpoint never overrides task expiry or cancellation. Suitable incumbent task infrastructure may implement the same contract.

UI progress derives from durable task transitions. Read-only POC workers have no production action path. This phase does not require a Temporal workflow ID, history or task queue.

### Action-phase reference: Temporal, if adopted

The following recovery table and Temporal activity/queue details apply only after adopting Temporal for the action phase. Temporal owns incident/task/action lifecycle and dispatch after intake handoff; PostgreSQL retains intake/outbox, evidence metadata and UI projections rather than independently scheduling the same work. An incumbent workflow engine may replace Temporal if it meets the same contracts. Drain or explicitly cancel/reconcile POC tasks before handing their scope to the new dispatcher; no task has two lifecycle owners.

LangGraph owns bounded reasoning and checkpoints inside an investigation task; it does not grant permissions or independently schedule production actions. The native Temporal/LangGraph integration remains Public Preview in the reviewed documentation, so this design uses an ordinary service/activity boundary [4].

| State | Authority | Recovery rule |
| --- | --- | --- |
| Accepted request and undispatched intake | PostgreSQL receipt/outbox with unique scoped request key | Retry starting the same stable Temporal workflow ID; reconcile ambiguous start responses |
| Incident/task/action lifecycle after dispatch | Temporal workflow history | Replay deterministic transitions; no LLM or network I/O inside workflow code |
| Investigator checkpoint | LangGraph PostgreSQL checkpointer, keyed by task and attempt generation | A single valid lease writer resumes; it cannot revive a cancelled parent or superseded generation |
| Evidence objects and metadata | Evidence service/object store/PostgreSQL | Immutable observation identity, checksums, timestamps, scope and provenance |
| Side-effect outcome | Executor action ledger plus authoritative target observation | Reconcile intent and target before retry; workflow completion cannot manufacture an external outcome |
| UI progress | PostgreSQL projection of versioned workflow events | Idempotent updates, lag/last-update visibility, replay/rebuild; never use the projection to authorize actions |

A Temporal activity invokes a bounded LangGraph task using the same task ID, carries a heartbeat/cancellation contract, and returns evidence/result references. Retrying it resumes or retrieves that task rather than starting an unrelated agent. Target services enforce task generation, authorization and cancellation on each tool operation, including when an old worker remains alive after losing its lease. A repeated model request can still incur duplicate provider charges; budgets account for attempts.

Use named queues by role and trust boundary: coordinator, operations-read, integration-read, engineering-analysis, engineering-sandbox, action-execution, and delivery-verification. These are logical capabilities; combine compatible read-only deployment processes initially, but never combine executor credentials or sandbox authority with investigators. Each always-on shared production queue needs redundant eligible workers across hosts/failure domains; pinned older worker versions need coverage too [3].

Queue names alone do not enforce roles. Only permitted worker identities may access each service/queue boundary; use separate namespaces or infrastructure boundaries where platform ACLs cannot isolate the required trust domains. Privileged commands still require executor authorization even if someone can submit a workflow task.

### Limits and cancellation apply in both phases

Delegate only useful tasks. A simple process-down incident may use one approved diagnostic path; an ambiguous multi-feed incident can fan out to the three teams. The coordinator validates dependencies and scope, waits within deadlines, preserves disagreements, and requests a discriminating test rather than polling agents indefinitely.

Maintain global and per-role admission budgets across replicas. Reserve incident capacity ahead of background capacity forecasts and skill builds. At saturation, coalesce related alerts without hiding distinct client impact, defer background work, limit new investigations, and report overload to on-call. Adding workers cannot overcome a saturated metrics backend or provider token quota.

Authenticated cancellation records a durable transition, prevents new tasks/retries/action dispatch, invalidates proposals, and requests bounded stopping of reads. Running effects retain their reconciliation lifecycle. Lost UI connections only disconnect presentation. Stream progress via SSE using persisted event IDs; reconnect with a cursor, or return a fresh snapshot if the cursor has expired. Reauthorize streams and evidence reads, including after entitlement changes.

## 5. AI model selection and routing

### Recommended starting models

Use **Claude Opus 5.5 (`claude-opus-5-5`) for the initial investigation quality baseline**, with **Claude Sonnet 5.5 (`claude-sonnet-5-5`) as the preferred routine-production candidate if it meets the same incident acceptance gates**. This is a benchmark recommendation, not evidence either model can diagnose this plant. The current Anthropic catalog documents both IDs and tool use; its model IDs are pinned snapshots [5].

| Work | Initial model policy | Why and escalation |
| --- | --- | --- |
| Alert ingestion, deduplication, routing from known labels, numeric detection | No LLM | Deterministic and testable; no provider dependency for paging |
| Free-text intent and bounded task planning | Sonnet 5.5 candidate; compare with Opus baseline | Requires correct scope and tool selection. Ambiguity asks for clarification; it does not expand authority |
| Operations/integration investigation | Opus 5.5 baseline, qualify Sonnet 5.5 for routine cases | Same role tools and evidence contracts regardless of model. Escalate difficult cases only within budget and deadline |
| Engineering skill/code development | Opus 5.5 candidate in the development sandbox | Test generated queries/scripts against independent fixtures. Human approval promotes; a successful model answer does not |
| High-volume text extraction/classification | Deterministic parser first; Haiku 5.5 (`claude-haiku-5-5`) optional | Introduce only if reduced cost/latency offsets another qualification surface. Do not let an unqualified cheap classifier suppress critical investigations |
| Difficult RCA challenger | OpenAI `gpt-6.1-sol`, with `gpt-6-astra` for harder benchmark cases | Evaluate on the same incident set; no automatic second-provider production path without data approval. Current OpenAI guidance recommends Responses API for these tool-calling models [6] |
| Private-hosted candidate | `Qwen/Qwen3.5-35B-A3B` via vLLM | Concrete Apache-2.0 open-weight candidate for constrained diagnostics/tool use, not an assumed substitute for frontier RCA quality. Test separately [7] |
| Permission, safety preconditions, failover decision gates, recovery verification | No LLM as authority | Deterministic code/policy and observed delivery conditions; models may explain results |

Start with one approved provider and one qualified model if possible. Role separation comes from identities, tools, task context and enforcement—not from buying a different model for each team. A second model can suggest counter-hypotheses but agreement is not independent evidence.

### Bounded inference and context

Maintain a release manifest containing provider, endpoint/region, exact model/deployment version, effort/decoding configuration, prompt version, tool schemas, skill digests and eval dataset version. Resolve aliases before release. Provider deprecation or a changed model forces requalification, not silent substitution.

The candidate IDs were sourced from official documentation when drafted; that lookup is not plant qualification. Before an evaluation run, recheck the exact account/region model availability, runtime/tool-parser compatibility and data-handling eligibility, and record them with the run. Architectural review of these choices and empirical qualification are separate gates; neither is implied by a valid catalog entry.

Use strict structured tool inputs/results where supported, plus server-side schema and domain validation. Handle refusal, truncation, invalid arguments and unavailable evidence explicitly. Schema-correct JSON can still contain an incorrect target, fabricated evidence reference or false causal claim. Resolve every cited evidence ID and validate its scope and timestamp [8].

Provide a compact case packet: request and authorized resources, topology slice, changes, selected metric windows, peer/control comparison, approved runbook snippets, contradictions and unknowns. Raw histories stay in evidence storage. Do not use a million-token context window as a telemetry sink. Preserve contradictory observations when summarizing; the investigator can request bounded additional evidence.

The LLM proposes only allowlisted tool calls. Tool execution happens in our services, not provider-hosted shells or unrestricted browsing. Disable unneeded hosted tools. No direct provider connection to plant administrator APIs, credentials, arbitrary URLs or MCP servers discovered in untrusted text.

Application admission enforces call count, total tokens including reasoning, tool cost, concurrency and elapsed-time budgets. For provider errors, distinguish overload from quota/authentication/policy refusal, respect retry hints, and use bounded backoff. Do not route a safety refusal through another provider to evade it. Fallback is allowed only to a separately qualified model/endpoint approved for the same data class; otherwise retain evidence and hand off to engineers.

### Private versus hosted deployment

**If approved sanitized operational evidence may leave the network:** use an enterprise API in an approved region/processing tier. Verify endpoint-specific retention, prompt caching, tracing and residency rather than assuming “not used for training” means no retention. OpenAI documents default abuse-monitoring retention and separately approved controls; `store: false` alone is not a universal zero-retention guarantee [9]. Obtain the corresponding contractual and technical checks for the chosen provider.

**If evidence must remain on premises:** use dedicated GPU inference hosts, vLLM, an authenticated internal gateway, and mirrored/pinned model weights/tokenizer/tool parser/runtime. The Qwen model card and vLLM recipe provide a starting point, but their example GPU counts and precision choices are not a production capacity plan. The recipe documents a single-GPU FP8 route; qualification must include memory, context length, concurrent sequences, prefill and decode throughput, tail latency and quantization quality [7].

For scale intuition only, 35 billion parameters at 2 bytes each require about 70 GB for weights alone before runtime/KV-cache overhead. “3B active parameters” does not mean only 3B parameters must be stored. Multiple replicas may be needed for concurrency and availability even if one model fits on one host. Never run inference on latency-sensitive plant machines. Do not copy privileged containers or remote-code execution flags from a model example without security review.

Keep private inference from silently falling back to a public endpoint. If the private candidate cannot meet diagnostic quality, narrow its duties to qualified tasks and retain human RCA rather than claim equivalence.

### Knowledge and numerical models

Start with reviewed runbooks and relational/full-text search over a small curated corpus. Retrieve by service, version, symptom and role before semantic similarity. Add pgvector and a qualified embedding model only if held-out retrieval tests demonstrate a need; enforce access filtering before retrieval/ranking and before presenting results. Retrieval provides procedures, not live facts.

No initial fine-tuning is required. First improve evidence contracts, skills and evaluations. Consider fine-tuning only after collecting reviewed, permissioned examples and demonstrating a recurring gap that retrieval/tool changes do not solve; keep incidents separated by family/time in evaluation.

Numerical anomaly detection and capacity forecasting are separate from LLM inference. Start with recording rules, session-aware robust baselines and explicit headroom calculations. If justified, engineering can use Python/scikit-learn or statsmodels for validated forecasting/change detection with prediction intervals and drift checks. A Flink/Kafka event-time pipeline is conditional on measured stateful stream-processing needs; ordinary metrics/rules do not require it.

## 6. Size telemetry, model work and queries independently

Measure unique active series, samples/second, label churn, histogram expansion, logs/events per second, retention and query patterns by site. Do not confuse wire message rate with sample rate. Estimate raw messaging bandwidth separately from ingress/egress bytes, fan-out, framing and replay amplification.

For metric groups, sample rate is \(R_s = \sum_i S_i / \Delta_i\), where \(S_i\) counts emitted scalar series and \(\Delta_i\) is the collection interval in seconds. Classic histogram buckets add multiple series; native histogram sample bytes vary. Count HA duplicates separately from logical unique series.

**Illustrative telemetry load, not measured production:**

| Scenario | Arithmetic | Consequence |
| --- | --- | --- |
| 20,000 processes, 100 scalar series each, all at 10 seconds | 2,000,000 / 10 = 200,000 samples/second | 17.28 billion samples/day before host metrics and replication |
| Same process/series counts, 20 series at 1 second and 80 at 10 seconds | 20,000 × (20 + 8) = 560,000 samples/second | 48.384 billion samples/day; 2.8 times the original rate |

These are workload examples, not proposed cadences. Engineering must price per-metric tuning against a fleet-wide collection budget. Storage bytes, CPU and node count require measurements of the selected backend/encoding and retention; sample counts alone are insufficient.

For inference, let admitted incident rate be \(\lambda\), mean calls across all roles per incident be \(k\), and mean call duration be \(T\). Average in-flight calls are approximately \(\lambda kT\) in a stable system. This is an average occupancy estimate, not tail-latency or burst capacity. Model tokens/minute, provider request limits and query concurrency impose additional limits.

**Synthetic sizing example:** 0.1 admitted incidents/second × 6 calls total per incident = 0.6 calls/second. At 20 seconds/call, average occupancy is 12 calls. At 12,000 input and 1,000 output tokens/call, demand is 432,000 input and 36,000 output tokens/minute, before retry/escalation overhead. Add measured variance and loss-of-worker/provider headroom; do not interpret 12 as a safe concurrency cap.

Track tokens and cost per *useful completed investigation*, including repeated context, failed attempts and escalations. Use contracted input/output/cache/reasoning billing, not a fixed price in the architecture. High market traffic primarily stresses telemetry and detection; only correlated, admitted cases reach models.

Set latency objectives separately for alert creation, direct paging, request acceptance, first evidence, first useful diagnosis, approval wait, execution and verified recovery. Run peak, sustained, burst and failure-recovery tests. No server/GPU count or subsecond AI-response promise is defensible until these workloads are measured.

## 7. Action safety and high availability

An action proposal names the runbook digest, exact targets, typed parameters, expected topology/config generation, evidence, expiry, approval requirements and postconditions. The executor validates it immediately before dispatch and records durable intent. A restart, failover and telemetry adjustment are different domain operations, not a generic shell command with different text.

The workflow admission gate and executor dispatch check must serialize against cancellation/revocation for the same action. Define the dispatch linearization point: cancellation before it denies execution; after it, mark the action dispatched/unknown and reconcile. Closing a UI, cancelling an RPC or timing out an activity cannot retract an external command.

Locks are per actual shared resource/redundancy group and must account for manual operations and existing automation. A PostgreSQL lock coordinates participating services only; it cannot fence an old plant leader or an operator acting outside the protocol. If the target lacks safe fencing/generation checks, keep unsafe failover manual. Never equate a Temporal retry with exactly-once external execution.

Use single active control ownership per resource group with explicit failover and target-enforced fencing. Do not begin with independent active-active cross-site mutators. A standby control plane must reconcile action intents, generations and target state after recovery before enabling writes. Database backup/PITR, object-store durability and workflow recovery require restore drills and agreed RPO/RTO; lost audit/action state blocks blind resumption.

| Failure | Required behavior |
| --- | --- |
| LLM timeout, quota or provider outage | Independent pages and protection continue; bounded retries/fallback; investigation reports blocked with evidence preserved |
| Metrics backend overload or stale results | Stop expensive queries; report visibility loss; no action based on stale evidence; independent probes/page path remain |
| Temporal or PostgreSQL unavailable | Stop accepting work that cannot be durably recorded; deny new AI writes; existing plant protection/manual procedures continue |
| Executor acknowledgement lost | Mark outcome unknown, inspect target using action/generation identity, then decide whether retry is safe |
| Site/WAN partition | Retain supported local protection and paging; do not infer standby safety from a missing primary heartbeat alone |
| Policy, approval or audit unavailable/revoked | Deny new writes; previously dispatched operations stay under authorized reconciliation |
| Temporary telemetry change expires | Use supported target-local TTL or previously authorized restoration; otherwise mark restoration overdue and escalate; do not overwrite newer operator changes |
| Skill or model release revoked | Deny new affected work; stop tasks at safe boundaries; reconcile effects; retain versioned audit evidence |

Observe the control plane independently: queue age, workflow/task failure, dead worker leases, model latency/429s/tokens, tool-denial counts, evidence age, projection lag, pending/expired approvals, unknown action outcomes and telemetry overhead. Deliver its critical pages through the existing non-AI route.

## 8. Qualification before trusting models or automation

Create engineer-reviewed incident packs containing authorized metric windows, topology, change history, misleading evidence, allowed actions and independently maintained expected outcomes. Include healthy market-open bursts, session silence, upstream loss, relay overload, slow consumer, collector failure, restart/PID reuse, stale topology, clock skew, bad deployment and split-brain risk. Keep fault labels inaccessible to agents.

Compare the deterministic runbook baseline, one investigator, and specialist delegation using identical evidence/tools/budgets. Evaluate Opus, Sonnet and the selected challenger on the same held-out cases; repeat trials to expose variability. Do not choose a model solely by public coding benchmarks, average score or stylistic fluency.

| Gate | What must be measured or demonstrated |
| --- | --- |
| Diagnosis | Evidence-supported findings, contradictions, incorrect causal claims, correct abstention, and engineer-rated usefulness per incident family |
| Model/tool contract | Invalid/refused/truncated output, invented evidence, wrong units, unsafe targets, prompt injection and role/tenant escalation |
| Information authorization | Narrow viewer reopening a broad incident, revoked entitlements, cached results, streamed summaries and retrieval across client boundaries |
| Durable lifecycle | Crash before/after dispatch, duplicate delivery, lease expiry with old worker alive, cancellation races, stale approval and version rollback |
| Plant protection | No prohibited action executes; verify actual target effects, sequence/freshness and unaffected controls, not just API status |
| Throughput | Query load, telemetry overhead and delivery tail latency under market peak, alert storm, one worker/site loss and backlog recovery |
| AI economics | Useful-result latency distribution, cost per completed case, request/token quota exhaustion, escalation frequency and operator fallback |

Safety gates are pass/fail for the finite release suite; useful-result targets are agreed with engineers against a measured baseline. Passing tests is not proof of zero hallucinations or zero production risk. An independent LLM critic cannot replace deterministic authorization or a human production approver.

Promotion order: synthetic lab; historical read-only replay; production shadow; one approval-gated low-risk runbook; bounded preauthorized routines. Change model/prompt/tool/skill/policy versions through the same relevant regression gates, with a rollback and revocation path.

## 9. Engineering work packages and unresolved inputs

**Deferred follow-up:** the AI implementation author prepares a self-contained Stage 1 lab specification after the owner settles the reference architecture, with operations/integration/engineering review. It must define synthetic components, adapter interfaces, fault scenarios, a hidden fault oracle and acceptance tests. This correction does not supply that specification or authorize implementation; synthetic assumptions must remain distinct from confirmed production contracts.

1. **Operations:** select one recent incident and one healthy peak session; define supported evidence, incorrect diagnoses, meaningful progress and escalation behavior.
2. **Integration:** provide one supported deployment/rollback interface with idempotency, preconditions, safe stop, approval and verification semantics.
3. **Engineering:** map per-process metrics/topology, measure collection/query overhead, define cadence controls and capacity/replay headroom, and author the first telemetry skill fixtures.
4. **Platform/security:** choose allowed model/data path, service identities, execution isolation, durable service hosting, retention and recovery ownership.
5. **AI implementation:** build the scoped case packet/read tools, qualify models, implement task state/progress, then add the action ledger and failure tests in the lab.

Before infrastructure procurement, obtain peak messages/bytes per second by site and fan-out, end-to-end latency/loss objectives, unique active series and actual cadences, process lifecycle labels, telemetry-query capacity, retained log/event volume, incident arrival/burst profile, supported plant SDK/admin APIs, and data-egress/GPU constraints. These change capacity and adapter choices; they do not change the separation between delivery and AI.

## Sources and limits

Official product documentation inspected on 14 November 2026. Product descriptions support candidate selection, not measured plant performance or compatibility with an untested release combination.

1. [Grafana Mimir ingest-storage architecture and query freshness](https://grafana.com/docs/mimir/latest/get-started/about-grafana-mimir-architecture/about-ingest-storage-architecture/).
2. OpenTelemetry Collector: [scaling](https://opentelemetry.io/docs/collector/scaling/) and [resiliency and loss modes](https://opentelemetry.io/docs/collector/resiliency/).
3. Temporal: [task queues](https://docs.temporal.io/task-queue) and [worker deployment/performance](https://docs.temporal.io/best-practices/worker).
4. [Temporal LangGraph integration and Public Preview constraints](https://docs.temporal.io/develop/python/integrations/langgraph).
5. [Anthropic model IDs, capabilities and versioning](https://platform.claude.com/docs/en/models/overview).
6. [OpenAI deployment/model-selection guidance](https://developers.openai.com/api/docs/guides/deployment-checklist).
7. [Qwen3.5-35B-A3B model card and license](https://huggingface.co/Qwen/Qwen3.5-35B-A3B) and [vLLM serving recipe](https://github.com/vllm-project/recipes/blob/main/models/Qwen/Qwen3.5-35B-A3B.yaml). Pin and validate the exact recipe/runtime rather than using mutable main/nightly production dependencies.
8. [Anthropic structured output and strict tool use](https://platform.claude.com/docs/en/build-with-claude/structured-outputs).
9. [OpenAI API data retention controls and endpoint exceptions](https://developers.openai.com/api/docs/guides/your-data).
