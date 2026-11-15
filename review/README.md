# Reviews

Review comments on the design docs. Each reviewer, whether human or agent, signs their work so the discussion can be traced.

## Conventions

- **File name:** `YYYY-MM-DD-<reviewer>-<topic>.md`, for example `2026-11-14-claude-opus-5.5-design-review.md`.
- **Reviewer tag:** each review states its reviewer, the exact model ID for agents, and the commit it reviewed. It ends with a signature line, for example `[claude-opus-5.5]`.
- **Responses:** the doc author replies inline in the `> **Response**` block under each finding, with their name or agent tag and date. Use accepted, rejected (with reason), or deferred.
- **Follow-up rounds:** add a new dated file rather than rewriting an earlier review. Link back to the earlier file.
- **Resolution:** once a finding is resolved, record it in [docs/architecture-reviews.md](../docs/architecture-reviews.md).

## Index

| Date | Reviewer | Scope | Status |
| --- | --- | --- | --- |
| 2026-11-14 | `[claude-opus-5.5]` | [All docs at `ca47aaa`](2026-11-14-claude-opus-5.5-design-review.md) | Dispositions agreed; corrections confirmed in re-review below |
| 2026-11-14 | `[claude-opus-5.5]` | [Re-review of PR #2 at `94107a1`](2026-11-14-claude-opus-5.5-rereview-94107a1.md) | Confirmed at reviewed commit; author addressed two non-blocking nits afterward, awaiting owner approval |
| 2026-11-14 | `[claude-opus-5.5]` | [Lab spec, PR #3 at `350e75d`](2026-11-14-claude-opus-5.5-lab-spec-350e75d.md) | Author addressed B1 and six suggestions; corrections confirmed in re-review below |
| 2026-11-14 | `[claude-opus-5.5]` | [Lab spec re-review and eval proposal, PR #3 at `b3fd412`](2026-11-14-claude-opus-5.5-lab-rereview-evals-b3fd412.md) | B1 confirmed resolved; author incorporated staged eval additions, awaiting review of additions |
| 2026-11-14 | `[claude-opus-5.5]` | [Lab spec evals and Temporal note, PR #3 at `d2eacc4`](2026-11-14-claude-opus-5.5-lab-evals-temporal-d2eacc4.md) | Confirmed, no blockers; three non-blocking suggestions; ready for owner approval |
| 2026-11-15 | `[claude-opus-5.5]` | [Go plant lab, PR #4 at `9d56555`](2026-11-15-claude-opus-5.5-go-plant-lab-9d56555.md) | Author fixed B1 and addressed six suggestions; confirmed in re-review below |
| 2026-11-15 | `[claude-opus-5.5]` | [Re-review of PR #4 at `f0c45da`](2026-11-15-claude-opus-5.5-go-plant-lab-rereview-f0c45da.md) | B1 confirmed resolved; merged |
| 2026-11-15 | `[claude-opus-5.5]` | [Bounded AI replay, PR #5 at `5d3d1c6`](2026-11-15-claude-opus-5.5-bounded-ai-replay-5d3d1c6.md) | Author addressed four suggestions and answered both questions; confirmed in re-review below |
| 2026-11-15 | `[claude-opus-5.5]` | [Re-review of PR #5 at `61f0997`](2026-11-15-claude-opus-5.5-bounded-ai-replay-rereview-61f0997.md) | All four suggestions resolved; no blockers; ready for owner approval |
