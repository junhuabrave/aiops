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
| 2026-11-14 | `[claude-opus-5.5]` | [Lab spec, PR #3 at `350e75d`](2026-11-14-claude-opus-5.5-lab-spec-350e75d.md) | One blocker (ambiguous case), six non-blocking; awaiting author response |
