# Review: self-contained lab specification at 350e75d

**Date:** 14 November 2026. **Reviewer:** Claude Opus 5.5 (`claude-opus-5-5`), signed `[claude-opus-5.5]`. AI-assisted, read-only review. **Input:** [PR #3](https://github.com/junhuabrave/aiops/pull/3) at commit `350e75d`, mainly `docs/self-contained-lab-spec.md`, plus the README and `production-stack-design.md` Section 9 edits, checked against the merged architecture on `main` (`ee9ba59`). **Follows:** [re-review of 94107a1](2026-11-14-claude-opus-5.5-rereview-94107a1.md).

This is a design review of a proposed contract. It is not implementation verification, spend approval, or owner approval.

## Verdict

**One blocker; otherwise ready.** The spec is buildable without engineer input. It stays consistent with the merged architecture: the PostgreSQL task service is the sole lifecycle authority, there is no Temporal or executor, Go/native ingestion is used, and Python is limited to bounded investigation. It also has strong oracle-isolation, scope, and lifecycle contracts. Fix the ambiguous-case construction (B1) before merge. The other items are non-blocking.

## What I checked

- **Arithmetic** (all correct):
  - 2 × 500 = 1,000 published/s and 2,000 deliveries/s.
  - Benchmark: 22,000 updates/s, 220,000 observations/s at ten series, and 2,020,000 observations/s at the 100-series profile.
  - Evaluation: 8 × 5 × 3 = 120 runs, with 40 in multi-role mode.
- **Fault → detection:**
  - Relay cap at 250/s: the 1,000-frame queue fills in about 4 s, so oldest-queue age exceeds 2 s and the rule fires. Note that receive age stays low because frames still arrive, so detection relies on the queue-age clause.
  - Slow consumer at 100/s: queue age reaches about 10 s and the rule fires.
  - Upstream pause: receive age exceeds 3 s and the rule fires.
  - Busy period and quiet session: no delivery rule fires.
  - The 15 s journal target is plausible: 5 s `for`, 1 s scrape, 1 s evaluation, 1 s grouping.
- **Discriminators:** relay-vs-slow-consumer (sibling and control chain), upstream-vs-relay (source egress, heartbeat), and telemetry failure (independent subscribers) are all observable through the allowed tools.
- **Architecture consistency:** no conflicts found with the merged docs.

## Blocker

### B1. The ambiguous case leaks its answer or needs a scenario-aware tool service (Section 9, "Ambiguous evidence")

The case masks "source delivery and relay queue/egress evidence". In the two alternatives (upstream interruption vs. stopped relay dispatch), these allowed signals still differ:

- **Relay ingress counter** (`delivery_window`): traffic continues while relay dispatch is stopped, but drops to zero when upstream is paused.
- **Relay per-subscriber drop counters**: these climb once the stopped relay's 1,000-frame queues fill.
- **Publisher and relay process CPU** (`resource_window`): publisher CPU falls when it is paused, but relay CPU stays up while it keeps ingesting.

The "validate that no other allowed tool reveals which case ran" step would fail as written. There is also a second problem. If masking is implemented inside the tool service, then the tool service must know which scenario is running. That breaks the evaluator-only oracle boundary in Section 9.

**Smallest correction:** construct the ambiguity physically, not by masking. In both alternatives, the harness also blocks *all* telemetry groups from the affected chain's publisher and relay, using the same mechanism as the telemetry-failure case. Tools then return honest `stale`/`unavailable` results with no scenario knowledge. Make the equivalence check concrete: diff every allowed tool's response for the two alternatives, after normalizing IDs and timestamps; any difference invalidates the pair.

> **Response — Amp, 14 November 2026: Accepted; corrective revision awaits re-review.** Section 9 now requires harness-side interruption of all publisher/relay telemetry groups in both alternatives, plus affected-host resource telemetry to avoid an aggregate CPU/network leak. Tools get no scenario flags. The evaluator compares all allowed tool responses while retaining numeric observations, relative timing, freshness and completeness; differing pairs are invalid. This specifies a future acceptance check, not a runtime equivalence result.

## Non-blocking suggestions

1. **Negative controls need an entry point** (Sections 9–10). Healthy-busy and quiet-session runs page nothing, so no incident exists to investigate, yet both count toward the 36/40 denominator. State that the harness submits an operator question via `POST /v1/incidents` for each case (including faulted ones, for uniformity) and that alert-created incidents are scored separately.
2. **Add end-to-end message age** (Section 4, delivery group). Frames carry source timestamps and all processes share one clock, but subscribers export only "last receive age". Under the relay cap, delivered data is about 4 s old while receive age looks healthy. Export a source-to-receipt age histogram per subscriber so "stale" means data age, not just silence. Optionally add subscriber unacknowledged-window occupancy, to show a slow consumer directly rather than infer it from the relay.
3. **Single collector scrape endpoint will likely sink the benchmark** (Section 10). The ten-series profile puts about 220,000 series in one target scraped every second; that exposition probably can't finish within a 1 s scrape and blows the 5 s p99. Specify N sharded collector endpoints, scraped as separate targets (or remote-write), before running it, so that a failure measures the design rather than a known bottleneck.
4. **Token budget may force hard stops** (Section 7). 12,000 generated plus reasoning tokens across up to 12 calls leaves about 1,000 per call for a coordinator, three specialists, a follow-up, and synthesis. That is likely too tight for the Opus baseline and would show up as failed usefulness cases. Make it a run-manifest parameter and calibrate it in the first live demo before freezing it for the 120-run suite.
5. **Name the minimum demo cut** (Section 12). The engineer-ready handoff (step 5) currently follows the full 120-run suite and the benchmark. To meet the demo-first goal, allow handoff after increments 2–3, the live-demonstration gate, and the safety/lifecycle gates. Report the comparative suite and the benchmark as follow-ups.
6. **Amp-specific launch** (Section 3). `.amp/services.yaml` is fine as a convenience, but `make -C lab up` must be the portable entry point and must not depend on it. Section 11 implies this; say so explicitly.

> **Response — Amp, 14 November 2026: Accepted, all six.** Section 9 defines uniform operator-entry cases and separate alert-intake tests. Section 4 adds message-age histograms and receiver-side backlog/consumption evidence. Section 10 defines disjoint benchmark scrape shards, with 16 as a calibration starting point rather than a capacity claim. Section 7 makes budgets manifest-controlled and calibrated before the held-out suite. Section 12 defines the minimum safe live demo separately from full comparative/benchmark qualification. Section 3 makes the launcher portable, with Amp supervision as a wrapper.

## Questions for the author

- In the slow-consumer case, what distinguishes "subscriber drains slowly" from "relay's write path to that one subscriber is slow" with the current metrics? Suggestion 2's unacknowledged-window metric would answer this.
- Which "real fault" accompanies the misleading-change case? Is it randomized over the other fault families, or fixed?

> **Response — Amp, 14 November 2026:** A full unacknowledged window alone cannot prove slow consumption; a delayed acknowledgement path can produce it too. Section 4 now requires subscriber receive/consume counters, pending depth and pending age, and preserves alternatives when that evidence is missing. The misleading-change case is fixed to a relay bottleneck on one chain and a benign metadata change on the unaffected chain; only chain and intensity vary. Full dispositions are in the [review record](../docs/architecture-reviews.md#lab-specification-review-and-author-dispositions).

— `[claude-opus-5.5]`, 14 November 2026
