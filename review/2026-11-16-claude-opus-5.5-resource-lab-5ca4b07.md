# Review: standalone resource troubleshooting lab at 5ca4b07

**Date:** 16 November 2026. **Reviewer:** Claude Opus 5.5 (`claude-opus-5-5`), signed `[claude-opus-5.5]`. AI-assisted, read-only review. Go is not run locally, and the hosted CI does not build this module, so nothing here has been executed by the reviewer. **Input:** [PR #7](https://github.com/junhuabrave/aiops/pull/7) at commit `5ca4b07`, all new files under `lab/resources/`, diffed against `main` (`a366560`). **Follows:** [PR #6 review](2026-11-15-claude-opus-5.5-isolated-workers-188e906.md).

This is not implementation verification or owner approval.

## Verdict

**No blockers.** The diagnostic boundary is clean:
- `observe` takes only a PID, path and optional device. It never sees the scenario, and the harness setup line (including the ENOSPC truth) stays outside `Report`.
- Missing, reset or changed evidence becomes `visibility_limited`, never healthy.
- Every finding states what it does *not* prove.
- Disk-full runs only after verifying an 8 MiB, 64-inode tmpfs in a private mount namespace.

The main gap is reproducibility. The documented E2E command depends on how `/tmp` is backed and on whether the host allows user namespaces, and CI runs none of this module, not even the unit tests. Suggestions 1–3 cover this.

## What I checked

| Area | Result |
| --- | --- |
| Rate arithmetic | **Correct.** Process CPU is Δ(utime+stime) / `CLK_TCK` / window × 100. Host busy is Δtotal minus idle minus iowait, over Δtotal; guest time is correctly not double-counted. PSI is Δµs / (window × 10⁴). Device busy is Δms / (window × 10). |
| `/proc` parsing | **Correct.** `stat` fields are taken after the last `)`, so a `comm` containing spaces or parentheses is safe. utime, stime, starttime and rss are at the correct offsets. A zombie or dead process is rejected. A change in start ticks suppresses process rates, which guards against PID reuse. |
| `st_dev` to device | **Correct.** The glibc major/minor decoding is right. A partition resolves through `/sys/dev/block` to its own diskstats row. Overlay and tmpfs paths correctly fall back to "unassessed" rather than guessing. |
| Scenario separation | Each case's stimulus maps to its intended code, and the codes don't overlap. The ambiguous case averages about 50% CPU, so it raises `variable_cpu_activity` and not `process_cpu_busy`. It holds a stable 64 MiB, so it raises `resident_memory_observed` and not `rss_growth`. |
| Containment | User, mount and PID namespaces are used with `--kill-child=SIGKILL`. A process-group kill fires at window + 5 s, and the coordinator and worker have their own deadlines. The I/O file is unlinked right after it is opened. Each rlimit is set on both soft and hard limits. `_load` refuses to run unless its parent is the PID-1 coordinator. |
| Scope | The PR changes only `lab/resources/`, as its description says. |

## Non-blocking suggestions

1. **On a tmpfs `/tmp`, the I/O case generates no storage I/O** (`runIsolated`, `load`). The scratch path comes from `os.MkdirTemp("", …)`, which resolves through the test's `TMPDIR` to `/tmp`. Debian 13, the lab's install target, mounts `/tmp` as tmpfs by default on new installs. On tmpfs, `/proc/<pid>/io` `write_bytes` stays at 0 and `fsync` does nothing. The `io` E2E assertion (`process_write_activity`) would then fail on a stock Debian 13 host even though the code is correct. The diagnosis itself would be right, since it would not claim writes, but the harness would be testing nothing. The PR's measured 15 MiB/s suggests the author's `/tmp` is disk-backed. Fix: have the I/O case refuse a tmpfs path, the same way disk-full refuses anything else, or take an explicit disk-backed scratch directory.
2. **Hosts that restrict user namespaces will likely refuse `unshare --user`** (`runIsolated`). On Ubuntu 23.10+ and GitHub's `ubuntu-24.04` runners, `kernel.apparmor_restrict_unprivileged_userns=1` lets an unconfined process create a user namespace but strips its capabilities. `--mount-proc` and the private tmpfs mount should then fail with EPERM. PR #6's `bwrap-userns-restrict` profile covers `bwrap` only, not `unshare` or this binary. The failure is safe ("no unsandboxed fallback"), but say in the README which hosts are supported, and plan the profile, or a launch through `bwrap`, before this joins CI.
3. **Run the unit tests in CI now.** The five unit tests in `diagnose_test.go`, including `TestScenarioNamesCannotChangeAssessment` and `TestMissingResetAndChangedEvidenceNeverBecomesHealthy`, need no namespaces or privileges. One more `go -C lab/resources test -race ./...` line inside the existing Debian container step would cover the diagnostic contract on every PR, leaving only the E2E as local-only. Today the green check on this PR says nothing about this module.
4. **Nit:** the per-UID lock file is opened in shared `/tmp` without `O_NOFOLLOW`. `fs.protected_symlinks` blocks the obvious symlink attack on standard distributions, but `$XDG_RUNTIME_DIR` or `O_NOFOLLOW` removes the dependency.

## Questions for the author

- Is `/tmp` on the orb disk-backed? If so, that explains why the I/O case passes locally and suggestion 1 is untested rather than wrong.
- Is this module meant to feed PR #6's isolated workers as a new tool group (`resource_window` from real `/proc` data)? If it is, the read-budget and scope rules will need to cover arbitrary-PID diagnosis, since `diagnose --pid` can currently observe any process the caller can read.

— `[claude-opus-5.5]`, 16 November 2026
