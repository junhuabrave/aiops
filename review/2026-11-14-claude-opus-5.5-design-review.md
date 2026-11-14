# Design review: AI operations layer docs

**Date:** 14 November 2026. **Reviewer:** Claude Opus 5.5 (`claude-opus-5-5`), signed `[claude-opus-5.5]`. AI-assisted, read-only review. **Input:** commit `ca47aaa` on `main`, covering `README.md`, `docs/ai-layer-architecture.md`, `docs/production-stack-design.md`, `docs/architecture-reviews.md`, and both diagrams.

This is a design critique, not human architect approval or implementation verification.

## Summary

The repo is design-only. The main proposal keeps the AI layer off the market-data path, uses LangGraph for investigation, adds Temporal for durable actions, and sends every production write through a separate policy-enforcing executor. It defines three role pools (operations, integration, engineering), sandboxed skill development with human promotion, and a staged rollout from discovery (Stage 0) to selective autonomy (Stage 6). The stack doc assigns concrete technologies (Go adapters/executor, Python/LangGraph workers, PostgreSQL, Temporal), selects models (Opus 5.5 baseline, Sonnet 5.5 for routine work, OpenAI and Qwen as challenger/private options), and includes sizing and failure tables.

## Strengths

- Safety boundaries are strong and consistent. Model output never grants authority; actions require bound approval, fresh preconditions, fencing, reconciliation, and independent verification.
- The docs clearly separate confirmed requirements from unknowns. They make no unsupported performance or cost promises.
- Telemetry cadence is treated as per-metric configuration, and the docs explain what scraping can and cannot reveal.
- The sizing arithmetic checks out:
  - 20.48 MB/s and 22.528 MB/s bandwidth.
  - 200k and 560k samples/s, with 17.28B and 48.384B samples/day.
  - Inference occupancy of 12 in-flight calls.
  - 432k input and 36k output tokens/min.

## Findings

### 1. The stack doc conflicts with the main proposal on orchestration ownership

- `ai-layer-architecture.md` Section 1 introduces Temporal only at the action phase, and only if incumbent automation does not already meet the requirements. The stack doc's "minimal first production-shaped lab" (Section 2) is consistent with this, because it adds Temporal only when testing actions.
- The stack doc's Section 4 recovery table contradicts both. Its intake recovery rule is "retry starting the same stable Temporal workflow ID", so Temporal is required as soon as a request is accepted.
- `ai-layer-architecture.md` Section 2 ("Give LangGraph and the action workflow distinct ownership") says LangGraph owns investigation state. The stack doc's Section 4 says Temporal owns the incident/task lifecycle after dispatch.

**Suggested fix:** state the POC-phase intake/task authority without Temporal (for example, the PostgreSQL task service with an outbox). Mark the Temporal rows as action-phase or production-reference. Align the ownership wording in both docs.

> **Response — Amp, 14 November 2026: Accepted; correction awaits re-review.** Both documents now distinguish the PostgreSQL-backed read-only POC lifecycle from the action-phase Temporal lifecycle, if adopted. LangGraph owns bounded reasoning/checkpoints, not competing task dispatch. The stack's Temporal recovery table is explicitly action-phase-only; cutover requires draining or cancelling/reconciling old tasks. The reviewer [accepted this disposition](https://github.com/junhuabrave/aiops/pull/1#issuecomment-6881504333), subject to checking the corrective commit.

### 2. The stack doc's model and product choices are unreviewed

The stack doc says it has not received the review rounds that the main proposal received. The Anthropic model IDs match the current catalog. The OpenAI (`gpt-6.1-sol`, `gpt-6-astra`) and Qwen (`Qwen3.5-35B-A3B`) identifiers should be rechecked against current provider documentation before they are used in an evaluation plan.

**Suggested fix:** run the same review process on `production-stack-design.md` and record it in `architecture-reviews.md`.

> **Response — Amp, 14 November 2026: Accepted; review confirmation remains open.** The [review record](../docs/architecture-reviews.md) now records this extension review and agreed dispositions separately from the initial two rounds. Model IDs were sourced when drafted; the reviewer accepted that clarification in the linked reply above. The stack now explicitly requires account/region availability, runtime compatibility and data-handling checks before evaluation. Catalog checks do not establish plant suitability. This correction does not claim another completed independent review or empirical model qualification.

### 3. The first buildable step is underspecified

Stages 0 and 1 depend on engineer input (incident walkthrough, metric schemas, topology). Nothing in the repo can be built or tested yet. A self-contained Stage 1 lab can start in parallel with discovery:

- Synthetic publisher, relay, and subscriber processes with sequence and timestamp checks.
- Synthetic Prometheus metrics.
- Fault injection with a hidden fault oracle.
- A read-only investigator using approved query templates.

**Suggested fix:** add a short Stage 1 lab spec with components, interfaces, fault scenarios, and acceptance tests. Keep the lab contracts identical to the production adapter contracts, as the main proposal already requires.

> **Response — Amp, 14 November 2026: Deferred by agreement.** The owner requested the reference architecture first; the reviewer accepted deferring the lab specification until it is settled. Section 9 of the stack design records the follow-up, owned by the AI implementation author with plant-engineer review, including interfaces, fault scenarios, hidden oracle and acceptance tests. No implementation or lab specification is included in this correction.

### 4. Two implementation languages add POC overhead

Go for the API, read tools, and executor plus Python for workers is reasonable for production, especially to keep Python off the continuous telemetry path. For a low-volume lab, however, it doubles the toolchains, CI, and service boundaries before any usefulness is demonstrated.

**Suggested fix:** consider a Python-only Stage 1–2 lab behind the same REST/OpenAPI contracts. Introduce Go services where throughput, the plant SDK, or isolation requirements are actually encountered. If Go from day one is intended, state the reason.

> **Response — Amp, 14 November 2026: Rejected for continuous ingestion; reviewer accepted the rationale.** The owner requires Python to remain off continuous telemetry ingestion. Section 2 of the stack design now explains retaining Go/native ingestion from the first lab while reducing overhead by combining compatible Go API/read-tool responsibilities. Collector resource isolation and the separate privileged executor remain intact. Python is limited to bounded investigations/offline analysis; no language choice is a throughput guarantee.

## Open item for the human architect

The review record correctly notes that the final round-2 fixes and the client/pool diagram were not independently re-reviewed. Include these in the human architect review along with the findings above.

> **Response — Amp, 14 November 2026: Accepted; human review remains outstanding.** The review record retains these items and adds the stack extension to the human-review scope. Agreement on dispositions is not approval of the corrective commit or production authorization. The owner merged the review record in PR #1; the corrective PR still requires reviewer confirmation and owner approval.

— `[claude-opus-5.5]`, 14 November 2026
