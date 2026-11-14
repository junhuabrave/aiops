# Architecture review record

This records the initial two sequential AI-assisted document reviews, Claude's all-document review in PR #1, and its corrective-revision re-review in PR #2. They are design critiques, not independent human approval, penetration tests, implementation verification, or evidence of production readiness. A human architect's peer review remains outstanding.

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

## Claude review: all documents, including the stack extension

**Date:** 14 November 2026. **Reviewer:** Claude Opus 5.5 (`claude-opus-5-5`). **Input:** [stack reference commit ca47aaa](https://github.com/junhuabrave/aiops/commit/ca47aaa99b3f2b8eba307592e33dbf1a2e4dcd59). Scope includes both architecture documents, README, this record and both diagrams. The [signed review](../review/2026-11-14-claude-opus-5.5-design-review.md) was merged by the owner in [PR #1](https://github.com/junhuabrave/aiops/pull/1).

The [author response](https://github.com/junhuabrave/aiops/pull/1#issuecomment-6881469403) and [reviewer reply](https://github.com/junhuabrave/aiops/pull/1#issuecomment-6881504333) agreed on all four dispositions. Claude subsequently [confirmed corrective commit 94107a1](https://github.com/junhuabrave/aiops/pull/2#issuecomment-6881709741) with no blocking findings in [PR #2](https://github.com/junhuabrave/aiops/pull/2); the [signed re-review](../review/2026-11-14-claude-opus-5.5-rereview-94107a1.md) records the exact scope and evidence.

| Finding | Agreed disposition and corrective status |
| --- | --- |
| POC/production orchestration ownership | Accepted and confirmed at 94107a1. Both docs specify PostgreSQL task-service lifecycle for the read-only POC, and Temporal lifecycle after action-phase adoption; LangGraph owns bounded reasoning/checkpoints. |
| Stack/model choices need their own review | Accepted and confirmed at 94107a1. This separate review is recorded; sourced IDs are distinguished from account/region eligibility and empirical qualification. Deployment qualification is future work. |
| First buildable lab specification | Deferred until the reference architecture is settled. AI implementation author owns the specification follow-up with plant-engineer review; Section 9 of the stack design records scope. No lab implementation is authorized here. |
| Python-only POC alternative | Rejected for continuous ingestion; reviewer accepted the owner's constraint. Go/native ingestion remains, compatible Go API/read tools may share a service, and the privileged executor stays separate. Rationale is now explicit in the stack design. |

**Post-review author update — Amp, 14 November 2026:** addressed both non-blocking nits by broadening this section's heading and explicitly including Go/native telemetry ingestion in the minimal lab list. Updated current review-status text; the original signed reviews and dated author responses remain historical records. These wording/status edits follow the reviewed commit and have not received another independent review; they change no architecture contracts.

**Outstanding:** owner approval before merge and human review of the final original round-2 fixes/client diagram and stack extension. No production behavior, performance or model suitability was tested by this review.

## Lab specification review and author dispositions

**Date:** 14 November 2026. **Reviewer:** Claude Opus 5.5 (`claude-opus-5-5`). **Input:** [lab specification commit 350e75d](https://github.com/junhuabrave/aiops/commit/350e75d31cd76fef2ffc6f365a47e734b3cfa7fc). The [signed review](../review/2026-11-14-claude-opus-5.5-lab-spec-350e75d.md) and [PR #3 verdict](https://github.com/junhuabrave/aiops/pull/3#issuecomment-6882425186) identify one blocker, six non-blocking suggestions and two author questions. The reviewer found the initial specification buildable without engineer input and consistent with the merged architecture, but did not approve it for merge with B1 outstanding.

**Author dispositions — Amp:**

| Item | Correction in the lab specification |
| --- | --- |
| B1: ambiguous case leaks ingress/drop/CPU evidence or requires scenario-aware tools | Accepted, pending re-review. Section 9 replaces selective masking with harness-side loss of all publisher/relay telemetry groups and affected-host resource telemetry in both alternatives. Tools remain scenario-unaware; normalized full tool-response comparisons must reject distinguishable pairs. No runtime equivalence has been demonstrated. |
| 1: negative controls lack incident intake | Accepted. Section 9 gives all comparative cases a neutral authenticated operator request; alert-created incidents are tested separately from the 120-run diagnosis denominator. |
| 2: arrival freshness hides old data; subscriber cause is ambiguous | Accepted. Section 4 adds source-to-receipt age histograms and receiver-side receive/consume/backlog evidence. Unacknowledged-window occupancy alone cannot distinguish a slow consumer from an acknowledgement-path fault. |
| 3: benchmark uses one oversized scrape target | Accepted as a design precaution, not a measured bottleneck. Section 10 specifies disjoint collector/export shards, an initial 16-shard calibration profile and frozen resource/scrape settings for scored runs. |
| 4: generated/reasoning budget may be too small | Accepted. Section 7 makes budgets run-manifest parameters, calibrated on development cases then frozen equally across comparison modes; limits cannot grow during a failing scored run. |
| 5: engineer handoff waits for full qualification | Accepted. Section 12 permits handoff after the functional, safety/lifecycle and live-demo gates, explicitly leaving comparative and capacity qualification pending. |
| 6: launch depends on Amp | Accepted. Section 3 makes `make -C lab up` portable, with Amp supervision wrapping the same launcher. |
| Question: misleading change's real fault | Fixed to a relay bottleneck on one chain plus a benign metadata change on the unaffected chain. Chain and intensity may vary; fault family does not. |

The signed review text is preserved with dated author responses added. **Outstanding:** Claude's re-review of the corrective commit, owner approval, and all implementation/evaluation evidence. These are documentation corrections, not a claim that B1's runtime equivalence test or any lab gate has passed.

## Human architect review

Please review the [proposal](ai-layer-architecture.md), particularly Sections 2, 4, 6, 8, and 10, and the [stack extension](production-stack-design.md), including the final original round-2 fixes and client/pool diagram. Resolve the action/workflow integration boundary, role authority, skill promotion, temporary telemetry restoration, production failure semantics, and minimum platform footprint against the actual plant. Product choices and production rollout require owner approval; this document provides neither.
