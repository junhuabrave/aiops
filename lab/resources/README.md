# Bounded Linux resource troubleshooting lab

Independent Go proof of concept based on `origin/main` at [a366560](https://github.com/junhuabrave/aiops/commit/a366560ac43b4a83ebcd3afa0eb6ee9286a56de0) (merged PR #6). This directory owns its module, CLI, loads and tests. It imports nothing from `lab/go`, starts no plant services, touches no dashboard/database files and requires no provider credentials, model calls or spending. It is a **deterministic evidence baseline**, not an AI agent or production diagnostic service.

## Run locally on a disposable Linux host

Requirements: Linux amd64, Go 1.24+, `getconf` (libc tools), `unshare` (util-linux), readable `/proc`, and permitted unprivileged user/mount/PID namespaces. `/sys/dev/block` enables direct filesystem-to-device mapping. No Go dependencies are downloaded. Tested with Go 1.24.4 and Linux 6.1 in this separate orb. Unsupported namespace/mount permissions fail closed; there is no unsandboxed load fallback. Do not weaken a shared host's security policy just to run the lab.

From the repository root:

```sh
go -C lab/resources build -o resource-lab .
mkdir -p lab/resources/results
lab/resources/resource-lab run --case all --duration 4s > lab/resources/results/scenarios.json
jq '.[] | {case: .harness_scenario, metrics: .diagnostic.derived, findings: .diagnostic.findings}' lab/resources/results/scenarios.json
```

`run --case cpu` selects one case. Available cases: `healthy`, `cpu`, `memory`, `io`, `disk-full`, `ambiguous`. Duration is constrained to 3–8 seconds per observation window; `all` runs sequentially. Exit zero means collection completed, **not** healthy or an intended stimulus necessarily observed. Exit nonzero means invalid invocation, cancellation or harness/setup failure. On failure the current CLI does not emit a partial suite report; run cases individually to retain successful earlier outputs. Output files and the binary are ignored by Git.

Use the read-only diagnostic on an independently selected process and filesystem:

```sh
lab/resources/resource-lab diagnose --pid "$$" --path . --duration 4s
# Optional explicit device, chosen from this host's /proc/diskstats:
lab/resources/resource-lab diagnose --pid "$$" --path . --device vda --duration 4s
```

The diagnostic reads `/proc/PID/stat`, `/proc/PID/io`, `/proc/stat`, `/proc/meminfo`, `/proc/pressure/*`, filesystem `statfs`/`stat`, `/sys/dev/block` and `/proc/diskstats`; `getconf CLK_TCK` supplies CPU tick units. It does not read workload arguments, process names for classification, harness controls, labels, logs or readiness/write-error truth. `observe` receives only PID, path, optional device and duration. It never probes a write, launches a load or changes the observed process. Missing/permission-denied sources become explicit errors rather than zeroes. A nonexistent PID/path can therefore produce a successful JSON report with `visibility_limited`.

## What the experiments actually demonstrate

| Case | Bounded stimulus | Evidence-based interpretation |
| --- | --- | --- |
| `healthy` | Idle process with small runtime overhead | No selected process symptom; unavailable observations still prevent an unconditional healthy finding. |
| `cpu` | One Go execution slot busy for the window | CPU-active process. One logical core is 100%; this does not imply all host CPUs are saturated. |
| `memory` | Touch and retain eight 8 MiB increments | RSS growth. No host exhaustion, OOM or leak is deliberately induced. |
| `io` | Repeated 1 MiB writes plus `fsync` to an unlinked, at-most-8 MiB file | Kernel-accounted storage write activity and, where mapped, device busy time. The cap does **not guarantee high disk utilization** on fast storage. |
| `disk-full` | Fill a new private 8 MiB tmpfs and verify ENOSPC with a separate one-byte file | Zero available capacity, independent of I/O activity. The diagnostic sees `statfs`, not the harness's write error. This is real ENOSPC, but not a physical disk failure. |
| `ambiguous` | Retain 64 MiB before sampling; busy CPU for half the window, then idle | Stable resident working set plus variable CPU activity; legitimate demand and faulty behavior cannot be distinguished without workload context. |

The CPU, RSS and I/O stimuli intentionally stop short of exhausting the host. High host CPU, low memory headroom, high device busy time, inode exhaustion and reserved-block cases have deterministic counterexample tests, **not live destructive fault qualification**. A diagnostic must not turn a case name into a finding when the actual stimulus is too small to cross its thresholds.

## Limits and cleanup

- One sequential load worker; per-UID advisory lock in the caller's `TMPDIR` prevents concurrent invocations sharing that directory. It is not a cross-user or adversarial admission policy; deliberately changing `TMPDIR` creates another lock scope.
- Each worker sets hard kernel limits: 2 GiB virtual address space (`RLIMIT_AS`), `ceil(window)+2` CPU seconds, 8 MiB maximum individual file size, 32 descriptors, and zero core-dump size. Go needs a large virtual reservation; **2 GiB is not an RSS limit**. The touched load payload is at most 64 MiB plus small buffers/runtime. The 96 MiB Go GC target is soft, not a cgroup memory guarantee.
- `GOMAXPROCS=1` bounds Go execution parallelism, not thread count or host affinity. CPU seconds cover the worker's threads. CPU/memory/IO host budgets are not delegated cgroups.
- I/O pacing sleeps at least 62.5 ms after each write/sync: at most 16 MiB/s after the initial 1 MiB write, and never more than 8 MiB file extent. Setup requires at least 64 MiB free. This is application pacing, not a kernel block-I/O quota; other processes' activity remains independent.
- Disk-full always mounts a **new** tmpfs in the private mount namespace, with an 8 MiB capacity and 64 inodes. Before any filling, the worker verifies tmpfs type, exact size and inode bound. It never tries to fill `/`, `/tmp` or a caller-selected filesystem. A separate probe avoids confusing the file-size limit (`EFBIG`) with `ENOSPC`.
- The worker has its own `window+1s` deadline; the coordinator kills it at `window+2s`; the outer process kills the whole process group at `window+5s`. PID-namespace exit and parent-death signals terminate descendants. SIGINT/SIGTERM are handled. As with any userspace timeout, a kernel task stuck in uninterruptible I/O may not exit until the kernel operation returns.
- Normal completion/cancellation reaps the load, exits the namespaces and removes the scratch directory. The I/O file is unlinked immediately; tmpfs disappears with its namespace. SIGKILL of the outer process can leave an **empty** scratch directory, and the small advisory lock file intentionally persists to avoid lock-inode races. Neither retains load payload. No automatic cleanup of other runs is attempted.

These are controls for trusted built-in experiments. The namespace is **not an untrusted-code security sandbox**: host files are visible, and network/syscalls are not comprehensively filtered. Do not connect worker-supplied code or production credentials.

## Structured evidence and interpretation

`diagnose` emits one report. `run` emits an array of experiment envelopes containing `harness_scenario`, `harness_limits`, `harness_setup`, `diagnostic` and `cleanup`. To connect a future evaluator, pass **only the `diagnostic` evidence**, never the harness envelope/ground truth.

The diagnostic report has `schema_version: 1`, `mode: read_only_deterministic`, PID/path/device scope, tick frequency, measured window duration, five timestamped raw `samples`, derived metrics with units in their keys, `findings` with supporting values and suggested next checks, and `limitations`. Original observations stay in the report; the findings do not replace them. There is no remote worker API, persistence layer, authentication or signed evidence format in this increment.

Classification markers (lab heuristics, not production alert thresholds): process CPU ≥60% of one core; host aggregate busy ≥90%; interval CPU ≥60% and <20% in the same window marks variation; net RSS growth ≥16 MiB; stable RSS ≥48 MiB is merely a working-set observation; process accounted writes ≥1 MiB/s; host available memory or filesystem available space <10%; zero filesystem bytes/inodes reported separately; device busy ≥80%.

Process start ticks must match in **every** sample. Missing samples, counter resets, exited processes and changed filesystem device identity suppress affected rates/conclusions. CPU aggregate excludes duplicate guest counters and separates iowait. RSS is resident pages, not heap ownership. Final capacity/headroom readings and window-averaged rates have different temporal meanings.

When possible, filesystem `st_dev` maps directly through `/sys/dev/block/major:minor`; the report records that provenance. Explicit `--device` selection does not claim a relationship to the path/PID. Overlay/tmpfs mapping is not guessed. Device counters include all users of that device, and busy percentage alone cannot establish saturation on parallel/stacked storage. Neither host iowait nor process writes alone proves a disk bottleneck.

Snapshots are non-atomic and short; they can miss spikes. `/proc` host scope can exceed the process's cgroup. Cgroup limits, throttling, swap/reclaim, OOM history, queues, per-operation latency, application latency and changes are not collected. Available host RAM cannot rule out a cgroup OOM. These remain suggested read-only follow-ups, not invented evidence or unsupported RCA.

## Verify

```sh
# Counterexamples, missing/reset/generation handling and threshold boundaries; no loads:
go -C lab/resources test -race ./...
go -C lab/resources vet ./...

# Explicit opt-in to all six real loads and safety checks (~30 seconds):
RESOURCE_LAB_E2E=1 go -C lab/resources test -race -count=1 -v ./...
```

The E2E test builds a normal, non-race-instrumented load executable: the race runtime's large virtual reservation is incompatible with the load's address-space cap. The test/report logic still runs with the race detector. Namespace failures are test failures, not skipped tests, once E2E is enabled. Performance-starved hosts can fail the "stimulus not observed" checks; that is not a reason to fabricate a finding or remove limits.

The repository's existing GitHub workflow runs the plant lab, not this independent Go module. Run the commands above to verify resources; a green plant workflow does not establish that resource tests ran. CI wiring is outside this directory's ownership in this increment.

The suite checks real hard-limit values, concurrent-run refusal, SIGTERM and SIGKILL parent death, empty/no scratch payload after cancellation, the outer deadline with a SIGSTOP-frozen coordinator, full tmpfs type/size, ordinary-filesystem fill refusal, and read-only diagnosis of nonexistent scope. Counterexample fixtures independently separate process/host CPU, growth/stable RSS/headroom, capacity/IO/device activity, reserved blocks, inodes and missing sources. No test starts the plant demo.

## Measured in this separate orb — 16 November 2026

`RESOURCE_LAB_E2E=1 go test -race -count=1 -v ./...` passed in 30.759s, and `go vet ./...` passed. A separate four-second-per-case run produced `results/scenarios.json`:

| Case | Observed result |
| --- | --- |
| Healthy | 0.75% process CPU, no RSS growth or process writes; no selected process symptom. |
| CPU | 100.24% of one core versus 25.58% host busy; `process_cpu_busy`. Slightly over 100% reflects tick/window sampling and runtime overhead, not all-host utilization. |
| Memory | RSS increased 64.44 MiB; 93.28% host memory remained available; `rss_growth`, not exhaustion. |
| IO | 15.00 MiB/s process-accounted writes; mapped `vda` only 2.90% busy; 91.26% filesystem space available; `process_write_activity`, **not disk saturation**. |
| Disk-full | Private tmpfs total 8,388,608 bytes, available/free 0; harness independently got ENOSPC; `filesystem_no_available_space`. Root filesystem was never filled. |
| Ambiguous | CPU intervals about 1–101%; RSS stable at 66.52 MiB; `variable_cpu_activity` and `resident_memory_observed`, not a leak or sustained CPU finding. |

PSI files are absent in this kernel, so **all six reports include `visibility_limited`**, including the healthy control. Missing pressure metrics are omitted, not zero. No live host-exhaustion, high-device-utilization or production-readiness claim follows from these results.

In the safety run, SIGTERM/SIGKILL stopped descendants in about 2.4/0.7 ms with no payload files left; stopped descendants can briefly remain as zombies pending the host's init reaper. Freezing the coordinator still ended the command at 8.01s via the outer kill deadline. These timings are measurements on this host, not service guarantees. `results/e2e.txt` retains the local test output and is ignored by Git. These measurements were collected before publication; no plant service or deployment was changed.
