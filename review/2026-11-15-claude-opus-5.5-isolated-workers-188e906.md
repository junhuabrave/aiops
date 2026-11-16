# Review: isolated specialist workers and durable read budgets at 188e906

**Date:** 15 November 2026. **Reviewer:** Claude Opus 5.5 (`claude-opus-5-5`), signed `[claude-opus-5.5]`. AI-assisted, read-only review. Go and Bubblewrap are not run locally; this review relies on the hosted CI result. **Input:** [PR #6](https://github.com/junhuabrave/aiops/pull/6) at commit `188e906`, diffed against `main` (`193bf6d`). **Follows:** [re-review of PR #5 at 61f0997](2026-11-15-claude-opus-5.5-bounded-ai-replay-rereview-61f0997.md).

This is not implementation verification or owner approval.

## Verdict

**At `188e906`: one blocker. At `3e8a07d`: B1 resolved, no blockers** (see [Update at 3e8a07d](#update-at-3e8a07d)). The four non-blocking suggestions stand, and only suggestion 1 is partly addressed. The broker design is right:
- Identity comes from the Go-owned pipe, never from the worker.
- Role allowlists and scope are enforced in Go.
- Citations must be fresh points that Go actually delivered, and canonical values are copied from Go.
- Every reservation is committed before any bytes are released, and none is refunded.
- Cancellation fences the run through the generation.

The blocker is that hosted CI fails on all three tests that start a real sandbox. The PR itself names hosted CI as the place this configuration must be validated, and that validation has not passed.

## What I checked

| Area | Result |
| --- | --- |
| Seccomp BPF (`sandboxFilter`) | **Correct.** The arch check kills foreign ABIs. The `JGE 0x40000000` check rejects x32. Each denied syscall uses a correct `JEQ`/skip-1 pair returning `ERRNO\|EPERM`. The x86_64 numbers match what the comments name: `shmget`, `socket`, `socketpair`, `clone`, `fork`, `vfork`, SysV, `sysinfo`, `ptrace`, `syslog`, `mount`, keyring, `unshare`, `perf_event_open`, `setns`, `process_vm_*`, `kcmp`, `bpf`, `userfaultfd`, `io_uring_*` and `clone3`. `execve` is correctly left allowed, because bwrap installs the filter before exec'ing Python. |
| Bubblewrap invocation | Sound. It uses `--unshare-all --unshare-user --disable-userns --cap-drop ALL --clearenv`. There is no `/proc`, `/dev` or project mount, and `/tmp` is a 1 MiB tmpfs. Python runs with `-I -B`. `prlimit` replaces itself with bwrap, so `CommandContext` and `--die-with-parent` kill the whole tree. |
| Authentication | The sandbox routes are registered without `authorized`, but the outer handler applies the same-origin and session checks to `/api/`. The tests assert 401 for unauthenticated POST, GET and cancel. |
| Fencing and leases | All writes go through `change()` under advisory lock 17012026. `sandboxOwned` checks state, generation, lease and deadline. After completion, a late heartbeat is fenced and cannot reopen the run. A cancellation is detected within about 1 s by the heartbeat failure. A lost lease is blocked and never replayed. |
| Budget arithmetic | 3 roles × (4,096 + 131,072) = 405,504 reserved bytes, as reported. There are 9 permits, under the cap of 12. |
| Evidence scope | `subscriber-1` scope excludes every point that has a relay peer, which is conservative. `telemetry_health` IDs never enter `delivered`, so they cannot be cited. |

## Blocker

### B1. Hosted CI fails on every real-sandbox test

[Run 43766430657](https://github.com/junhuabrave/aiops/actions/runs/43766430657):

- `TestSandboxHTTPAndIsolatedReplay`: state `blocked`, with only one permit used (`Calls:1`, `ReservedBytes:4096`).
- `TestSandboxWorkerCannotEscapeOrForgeAuthority`: "worker exited without a selection".
- `TestSandboxStopsSilentWorkerAndKeepsCharges`: "worker never started".

All three fit a worker that dies before it reads its assignment, which most likely means bwrap could not create its namespaces. A likely cause is that `ubuntu-latest` (24.04) sets `kernel.apparmor_restrict_unprivileged_userns=1` on the host. Container root has no `CAP_SYS_ADMIN` in the initial namespace, so its user-namespace creation is still restricted, and `--security-opt apparmor=unconfined` does not lift a host sysctl. I can't confirm the cause, because `cmd.Stderr = io.Discard` throws away bwrap's error message (see suggestion 1).

The fail-closed behavior is correct: no unsandboxed fallback ran and no result was published. The PR's isolation claims rest on tests that have so far passed only on the author's machine.

**Smallest correction:**
1. Make the cause visible (suggestion 1).
2. Fix the runner configuration. One option is to run this job on the host and run `sudo sysctl -w kernel.apparmor_restrict_unprivileged_userns=0` before starting the Debian container yourself. Another is a runner where unprivileged user namespaces are allowed.
3. Keep the tests failing, not skipping, when the sandbox cannot start.

## Non-blocking suggestions

1. **Keep a bounded operator diagnostic for sandbox startup** (`sandbox_worker.go`). Treating worker stderr as untrusted and never showing it to users is right. But today a broken host and a misbehaving worker produce the same "blocked" note and leave no trace. Two ways to fix this:
   - Capture the first ~2 KiB of stderr into the server log only, never the API.
   - Run a fixed startup probe (for example, `bwrap … /usr/bin/python3.13 -c pass`) at service start, so "sandbox unavailable" is reported separately from a protocol or budget failure.
2. **Reserve the actual reply size, not a fixed 128 KiB** (`specialist`). `len(b)+1` is known before the permit, so reserving it is still exact and committed before release. With the fixed reservation, a 512 KiB budget allows only three tool replies per run. The 12-call limit can never be reached, and the PR #5 budget of 30 tools can't carry over to a live model.
3. **The deadline starts at submission, so time in the queue uses it up** (`sandbox_runs.deadline`). Two active runs of up to about 30 s each, against a 90 s deadline, means a run more than about four places back can expire before it is claimed. That makes the 20-slot queue mostly nominal. Either set the run deadline at claim time with a separate queue TTL, or state that queued runs expire after 90 s.
4. **Nit:** the 1 s heartbeat overwrites the per-role note ("Starting isolated *role* replay") with a generic note, so progress is barely visible. Separately, the relay-total subtraction guard can't trigger with the current two profiles, because the relay is in scope only when both subscribers are. Keep it, but add a test so it stays correct when a new profile appears.

## Update at 3e8a07d

Amp pushed `cf84ac1`, `668da8d` and `3e8a07d` while this review was being written. I checked them before posting.

| Item | Result |
| --- | --- |
| B1 | **Resolved.** [Run 43767886403](https://github.com/junhuabrave/aiops/actions/runs/43767886403) passes at `3e8a07d`. The Go suite reports `ok` and both chains pass the full-chain and subscriber-1 isolated replays (three sandboxed specialists each, 405,504 reserved bytes, no provider calls). |
| Root cause | It had two parts, both now identified from real errors. First, Docker's read-only `/proc/sys` blocked bwrap from writing `max_user_namespaces` (fixed with `systempaths=unconfined`). Second, Ubuntu's AppArmor user-namespace restriction blocked loopback setup (`RTM_NEWADDR`). The guess in my B1 was half right: the AppArmor restriction was real, but my suggested fix, turning off the host-wide sysctl, would have been the weaker one. |
| Fix quality | **Good.** The workflow loads Ubuntu's packaged, scoped `bwrap-userns-restrict` profile, which allows only bwrap to create namespaces and strips capabilities from its children. It also asserts that the host-wide restriction stays at `1`. The container gets no Docker socket and no `--privileged`. The outer `systempaths=unconfined` exposure is documented as safe only on disposable hosted runners. The worker code and isolation flags are unchanged, and no tests are skipped. |
| Suggestion 1 | **Partly addressed.** A trusted, evidence-free startup probe in the test now shows startup stderr, which is what diagnosed the failure. The production path still discards stderr, so a broken host and a misbehaving worker still produce the same note in service. A startup probe at service start would close that gap. Non-blocking. |

## Question for the author (answered)

- Answered by the update above: the root tests ran on the host, not inside the outer Docker policy. No further question.

— `[claude-opus-5.5]`, 15 November 2026
