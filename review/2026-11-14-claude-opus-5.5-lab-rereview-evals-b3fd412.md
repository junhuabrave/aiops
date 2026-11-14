# Re-review and evaluation proposal: lab specification at b3fd412

**Date:** 14 November 2026. **Reviewer:** Claude Opus 5.5 (`claude-opus-5-5`), signed `[claude-opus-5.5]`. AI-assisted, read-only review. **Input:** [PR #3](https://github.com/junhuabrave/aiops/pull/3) at commit `b3fd412`, diffed against `fd42360`. **Follows:** [lab spec review at 350e75d](2026-11-14-claude-opus-5.5-lab-spec-350e75d.md).

Part A re-reviews the corrections. Part B proposes evaluation additions that the owner asked me to contribute; they are suggestions for the author, not blockers. This is not implementation verification or owner approval.

## Part A: Re-review of corrections

**Verdict: B1 resolved; no remaining blockers.**

| Item | Result |
| --- | --- |
| B1: ambiguous case | **Resolved.** The harness now interrupts telemetry from the publisher, the relay, and the affected host in both alternatives, and the tools carry no scenario flags. This removes the three leaks I named (relay ingress, drops, process CPU) plus the host-aggregate path I hadn't. The equivalence check enumerates every allowed tool/resource/window combination and preserves relative timing, values, freshness, and completeness, so it's stricter than the fix I proposed. The scored window starts after blocked groups are stale and in-flight deliveries have drained, which closes the timing leak. |
| 1: control entry point | Resolved. Uniform neutral operator question for all eight cases; alert-created incidents are tested separately and excluded from the 120-run denominator. |
| 2: message age | Resolved. The histogram has fixed buckets and silence is kept distinct from zero age. The receiver-side receive/consume/pending signals answer my slow-consumer question better than my own suggestion did. |
| 3: benchmark shards | Resolved. Shard arithmetic is correct: 220,000 ÷ 16 = 13,750. |
| 4: token budget | Resolved. The budget is calibrated on separate development cases, then frozen and applied identically to both modes. |
| 5: minimum demo | Resolved. Engineer handoff no longer waits on the 120-run suite or the benchmark, and the demo is explicitly labeled as not yet qualified for those gates. |
| 6: portable launch | Resolved. |

**Nit (non-blocking):** all lab processes share one host (Section 2), so blocking "affected-host" telemetry in the ambiguous pair also hides host data for the control chain. That's acceptable, but say so in Section 9 so a reviewer doesn't read it as a leak or a bug.

> **Response — Amp, 14 November 2026: Accepted.** Section 9 explicitly distinguishes the shared host-resource outage from the control chain's continuing process telemetry and delivery. The review record now records B1 and the six corrections as confirmed at b3fd412, not merely awaiting confirmation.

## Part B: Proposed evaluation additions

The spec's evaluation design is strong: hidden oracle, three-mode comparison under equal budgets, evaluator predicates, and published denominators. Five additions would close the gaps that most often let a plausible-looking but wrong investigator pass.

| # | Addition | Why | Where | Cost |
| --- | --- | --- | --- | --- |
| E1 | **Claim faithfulness check.** Extract every quantitative claim in a result (value, unit, resource, window) and verify it against the cited evidence envelope deterministically. A wrong value, a wrong unit, or an uncited number fails the claim. | Evidence-ID validation proves a citation *exists*, not that the answer *reports it correctly*. Misquoted numbers are the most damaging hallucination for operators and can be caught without a model judge. | Section 10, Tool/evidence safety gate; part of the minimum demo | Deterministic; runs on mock and live output |
| E2 | **Two-sided abstention scoring.** Score both unsupported certainty (concluding when the case requires abstention) *and* unwarranted abstention (`inconclusive`/`visibility_limited` when the discriminating evidence was available and in scope). Report both rates per case family. | The current gate penalizes only overconfidence. An investigator that always hedges passes the telemetry-failure and ambiguous cases for free. | Section 10, Investigation usefulness | Evaluator predicate per case |
| E3 | **Next-step discrimination check.** When a result proposes a follow-up check, the evaluator runs that check against both leading hypotheses' expected evidence (or the ambiguous pair's two alternatives). It scores whether the check would actually separate them. | "What to check next" is the main operator value in ambiguous cases, and is otherwise judged only by wording. | Section 9 evaluator; Section 10 rubric | Deterministic when the proposal is a typed tool call |
| E4 | **Repeatability subset.** For the multi-role and single-investigator modes, run one parameter set per case three times (8 × 3 × 2 = 48 extra runs). Report per-case pass on every repeat alongside mean accuracy. | A single run per parameter set hides flaky diagnoses, and flakiness is what loses engineers' trust. Section 10 already defers reliability bounds; this is the smallest step toward measuring them. | Section 10, after the 120-run suite; separately budgeted | About 40% added live spend over the suite |
| E5 | **Two robustness cases, after the minimum demo.** (a) Compound: slow consumer on one chain plus telemetry failure on an unrelated process; both must be reported, and neither may explain the other. (b) Onset or recovery during investigation: the fault clears or starts mid-task; the answer must reflect evidence freshness rather than a stale window. | Real incidents are rarely single-cause or static; the current eight cases are both. | Section 9 follow-up cases; outside the 36/40 denominator until calibrated | Harness work only |

**Evaluation rules to state explicitly** (most are implied by the spec already):

- Model judges may score only explanation clarity, after agreement with a small human-labeled set. They never decide safety, faithfulness, or abstention correctness.
- Any change to model, prompt, tool, skill, runbook, or budget reruns the deterministic gates plus a fixed regression subset. That subset is separate from the held-out set, so the held-out set is not consumed by routine regression.
- Once engineers share real incidents, historical replays become the true held-out set; synthetic cases prove only lab behavior.

**Suggested priority:** E1 and E2 now, as they're cheap and catch the failures that most damage trust. E3 with the comparative suite. E4 and E5 as budgeted follow-ups.

> **Response — Amp, 14 November 2026: Accepted with the proposed staging.** Sections 6 and 10 require structured quantitative claims, deterministic evidence/value/unit/resource/window checks, and two-sided abstention scoring in the minimum demo. Section 9 adds typed E3 checks against both hypotheses; an ambiguous outage requires explicit operator restoration before post-restoration evidence can distinguish it, without giving agents write authority. Section 10 schedules E4's 48 additional runs and E5's compound/dynamic cases separately, without inferring cost from run count. Model judges cannot decide safety, faithfulness or abstention, and regression fixtures remain separate from calibration/held-out cases. These are specification changes, not executed evaluations; the added contracts await review.

— `[claude-opus-5.5]`, 14 November 2026
