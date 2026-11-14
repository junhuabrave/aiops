# Review: lab spec evaluation and Temporal additions at d2eacc4

**Date:** 14 November 2026. **Reviewer:** Claude Opus 5.5 (`claude-opus-5-5`), signed `[claude-opus-5.5]`. AI-assisted, read-only review. **Input:** [PR #3](https://github.com/junhuabrave/aiops/pull/3) at commit `d2eacc4`, diffed against `aeb3b3a`. **Follows:** [re-review and eval proposal at b3fd412](2026-11-14-claude-opus-5.5-lab-rereview-evals-b3fd412.md).

This is not implementation verification or owner approval.

## Verdict

**Confirmed; no blockers.** E1–E5 are incorporated as proposed, with the agreed staging. The Temporal note is technically accurate. The record and status edits are accurate, and my earlier reviewer prose is unchanged (only dated author responses were added). From the reviewer side, PR #3 is ready for owner approval.

## Checks

| Item | Result |
| --- | --- |
| E1: claim faithfulness (Section 6, Section 10 safety gate) | **Confirmed.** Structured claims carry evidence ID, field, resource, window, statistic, value and unit. Derived calculations are allowlisted before the run. Most importantly, the minimum-demo UI renders narrative from validated fields rather than unchecked prose, which makes the boundary enforceable rather than advisory. Invalid output is reported as a validation failure, never "corrected". |
| E2: two-sided abstention (Section 10) | **Confirmed.** Denominators are eligible cases per family, with zero-eligible cases marked n/a. Healthy controls require a supported healthy conclusion. Genuine tool/provider outages are reported as unsuccessful, not relabeled as correct abstention. E2 applies to the live demo too, so the demo can't pass by hedging. |
| E3: next-step discrimination (Section 9) | **Confirmed.** A typed proposal with predicted differences is tested against independently specified evidence. For the ambiguous outage, the restoration prerequisite is right: no read can distinguish identical evidence, so restoring collection is a harness/operator step scored on separate post-restoration fixtures, with no agent write authority and no feedback into the live investigation. |
| E4/E5 and evaluation rules (Section 10) | **Confirmed.** 8 × 3 × 2 = 48, kept outside the 120-run denominator, with fresh task IDs and checkpoints reset per repeat. Compound and dynamic cases get separate rubrics outside 36/40. Model judges are excluded from safety, faithfulness and abstention. The regression subset is separate from calibration and held-out sets. |
| Temporal note (Section 7) | **Accurate.** The Workflow/Activity split, replay of recorded Activity results, at-least-once Activity retry before a result is recorded, and "one Activity wrapping LangGraph records only its completion" all match Temporal's documented semantics. Both new links resolve (HTTP 200 on 14 November 2026). Positioning the deferral as a footprint choice rather than a limitation, with earlier adoption preferred over rebuilding a workflow engine, is consistent with the merged architecture. |
| Shared-host nit | Resolved in Section 9. |

## Non-blocking suggestions

1. **Close the free-text numeral path in E1** (Section 6). Hypotheses, missing checks and runbook proposals are still free-text fields, so a model can embed a number there that no claim backs ("queue age ~12 s"). Require numerals in free-text fields to be placeholders bound to validated claims; reject or flag any unbound numeral. Directional claims ("queue growing", "sibling healthy") are scored by the rubric, not E1; if they should be machine-checked, express them as allowlisted derived calculations.
2. **Declare E3-eligible cases up front** (Sections 9–10). The usefulness gate applies E3 "where the case requires a follow-up". As with E2's sufficiency declaration, fix that per-case list in the rubric before the run, so eligibility can't be decided after seeing the answers.
3. **Optional, Temporal phase:** if one Activity wraps a long LangGraph investigation, Activity heartbeat details can carry a progress cursor (for example, the last durable checkpoint ID). A retried attempt then resumes rather than restarting. This only refines the existing checkpoint explanation.

— `[claude-opus-5.5]`, 14 November 2026
