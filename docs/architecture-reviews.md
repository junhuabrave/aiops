# Architecture review record

This records two sequential AI-assisted document reviews requested by the plant owner. They are design critiques, not independent human approval, penetration tests, implementation verification, or evidence of production readiness. A human architect's peer review remains outstanding.

## Round 1: architecture and safety consistency

**Date:** 14 November 2026. **Reviewer:** a separate read-only reviewer agent. **Input:** `docs/ai-layer-architecture.md`, SHA-256 `e818c18a38a87febca1d84b5fb3bfd6ea6bfee6c14a6f711e57e6438dc2ba35a`.

Scope: AI-layer boundaries, operational ownership, skill-development authority, execution failure modes, product footprint, and alignment with confirmed plant requirements. The reviewer read the document and README; product documentation and production capabilities were not independently revalidated.

| Finding | Disposition |
| --- | --- |
| The blanket prohibition on agent-authored runtime code contradicted promotion of agent-developed skills | Corrected Section 6: prohibit newly generated/unreviewed/unpromoted code. Approved version-pinned executable skills may run through controlled interfaces; promotion does not grant write authority. |
| Guaranteed restoration at telemetry expiry conflicted with fail-closed writes and outage behavior | Corrected Section 4: expiry initiates reconciliation; restoration authorization and outage behavior must be defined before the change. Overdue/unknown restoration escalates independently. |
| OPA and PostgreSQL were more prescriptive than other conditional product choices | Clarified Section 2: reference implementations, with suitable incumbent policy and task/evidence services preferred where they meet the contracts. |

Remaining discovery includes actual fencing and reconciliation interfaces, metric-level tuning feasibility, SLOs, and approval ownership. These block production action enablement, not publication of a discussion draft.

After this round, the owner asked how an LLM would front an operational client and orchestrate specialist pools. Section 2 now defines the authenticated front door, bounded coordinator, durable task service, role pools, evidence/progress API, and separation from market-data delivery. The second review must assess this addition as well as the corrections.

## Round 2: revised proposal and client orchestration

**Date:** 14 November 2026. **Reviewer:** a second separate read-only reviewer agent. **Input:** `docs/ai-layer-architecture.md`, SHA-256 `93aa9f92df59d3a64a164e8e37ab448c7696e3ddba1ede63b730fe98a2e995d7`.

Scope: first-round dispositions, client/coordinator/worker boundaries, authorization, durable lifecycle, telemetry restoration, skill promotion, and minimal product footprint. The reviewer confirmed all three first-round findings were resolved and found two further discussion-draft contract gaps.

| Finding | Disposition |
| --- | --- |
| Tool authorization alone did not cover stored evidence, progress streams, or synthesized answers | Corrected Section 2: preserve scope metadata and authorize every read, subscription, and answer against current viewer entitlements; incident IDs and shared records confer no access. |
| Cancellation did not define a durable client-visible lifecycle | Corrected Sections 2 and 9: cancellation/expiry prevents new delegation, retries, and action dispatch, invalidates pending proposals, and requests bounded stopping of reads. Reconcile already-dispatched effects and report their outcome separately. Closing a stream does not cancel work. |

These final corrections were checked by the author against both findings; they were not submitted for a third independent review. The focused client/pool diagram was added after the second reviewer read the proposal and was visually checked by the author. No production integration or implementation was verified by either review.

## Human architect review

Please review the [proposal](ai-layer-architecture.md), particularly Sections 2, 4, 6, 8, and 10. Resolve the action/workflow integration boundary, role authority, skill promotion, temporary telemetry restoration, production failure semantics, and minimum platform footprint against the actual plant. Product choices and production rollout require owner approval; this document provides neither.
