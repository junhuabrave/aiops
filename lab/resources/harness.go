package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"strconv"
	"syscall"
	"time"
)

const (
	mib        = 1024 * 1024
	tmpfsMagic = 0x01021994
)

var scenarios = []string{"healthy", "cpu", "memory", "io", "disk-full", "ambiguous"}

type Experiment struct {
	Scenario   string `json:"harness_scenario"` // Never passed to observe/assess.
	Limits     string `json:"harness_limits"`
	LoadReady  string `json:"harness_setup"` // Includes write-error truth, not diagnostic evidence.
	Diagnostic Report `json:"diagnostic"`
	Cleanup    string `json:"cleanup"`
}

func knownScenario(name string) bool {
	for _, candidate := range scenarios {
		if name == candidate {
			return true
		}
	}
	return false
}

func runHarness(ctx context.Context, name string, duration time.Duration) ([]Experiment, error) {
	selected := scenarios
	if name != "all" {
		if !knownScenario(name) {
			return nil, fmt.Errorf("unknown case %q", name)
		}
		selected = []string{name}
	}
	// A per-UID advisory lock prevents this harness from multiplying its budgets.
	lock, err := os.OpenFile(filepath.Join(os.TempDir(), "resource-lab-"+strconv.Itoa(os.Getuid())+".lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	defer lock.Close()
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		return nil, fmt.Errorf("another resource harness is active: %w", err)
	}
	executable, err := os.Executable()
	if err != nil {
		return nil, err
	}
	results := []Experiment{}
	for _, scenario := range selected {
		result, err := runIsolated(ctx, executable, scenario, duration)
		if err != nil {
			return nil, fmt.Errorf("%s: %w (no unsandboxed fallback)", scenario, err)
		}
		results = append(results, result)
	}
	return results, nil
}

func runIsolated(ctx context.Context, executable, name string, duration time.Duration) (Experiment, error) {
	var result Experiment
	path, err := os.MkdirTemp("", "resource-lab-")
	if err != nil {
		return result, err
	}
	defer os.RemoveAll(path)
	deadline, cancel := context.WithTimeout(ctx, duration+5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(deadline, "unshare", "--user", "--map-root-user", "--mount", "--pid", "--fork", "--kill-child=SIGKILL", "--mount-proc", executable, "_sandbox", name, path, duration.String())
	cmd.Env = []string{"PATH=/usr/bin:/bin", "GOMAXPROCS=1"}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true, Pdeathsig: syscall.SIGKILL}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
	cmd.WaitDelay = time.Second
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		return result, fmt.Errorf("isolated run: %w: %s", err, stderr.String())
	}
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		return result, fmt.Errorf("sandbox report: %w", err)
	}
	if err := os.RemoveAll(path); err != nil {
		return result, fmt.Errorf("scratch cleanup: %w", err)
	}
	result.Cleanup = "load reaped; PID/mount namespaces exited; scratch directory removed"
	return result, nil
}

func sandbox(ctx context.Context, name, path string, duration time.Duration) error {
	if os.Getpid() != 1 || !knownScenario(name) {
		return fmt.Errorf("sandbox requires isolated PID 1 and a known scenario")
	}
	if err := validDuration(duration); err != nil {
		return err
	}
	if err := syscall.Mount("", "/", "", syscall.MS_REC|syscall.MS_PRIVATE, ""); err != nil {
		return err
	}
	if name == "disk-full" {
		if err := syscall.Mount("resource-lab", path, "tmpfs", syscall.MS_NOSUID|syscall.MS_NODEV|syscall.MS_NOEXEC, "size=8m,nr_inodes=64,mode=0700"); err != nil {
			return fmt.Errorf("mount isolated 8 MiB tmpfs: %w", err)
		}
		defer syscall.Unmount(path, syscall.MNT_DETACH)
	}
	executable, err := os.Executable()
	if err != nil {
		return err
	}
	deadline, cancel := context.WithTimeout(ctx, duration+2*time.Second)
	defer cancel()
	worker := exec.CommandContext(deadline, executable, "_load", name, path, duration.String(), "1")
	worker.SysProcAttr = &syscall.SysProcAttr{Pdeathsig: syscall.SIGKILL}
	worker.Stderr = os.Stderr
	worker.WaitDelay = time.Second
	pipe, err := worker.StdoutPipe()
	if err != nil {
		return err
	}
	if err := worker.Start(); err != nil {
		return err
	}
	defer func() {
		worker.Process.Kill()
		worker.Wait()
	}()
	ready := bufio.NewReader(pipe)
	line, err := ready.ReadString('\n')
	if err != nil {
		return fmt.Errorf("load did not initialize: %w", err)
	}
	report, err := observe(deadline, worker.Process.Pid, path, "", duration)
	if err != nil {
		return err
	}
	return writeJSON(Experiment{
		Scenario:   name,
		Limits:     "one sequential worker, GOMAXPROCS=1; hard RLIMIT_AS=2 GiB, RLIMIT_CPU=ceil(window)+2 seconds, RLIMIT_FSIZE=8 MiB, NOFILE=32, CORE=0; retained load payload <=64 MiB plus buffers/runtime; I/O <=16 MiB/s after initial 1 MiB write with <=8 MiB file extent; worker wall deadline=window+1s, coordinator=window+2s, outer kill=window+5s; disk-full tmpfs=8 MiB/64 inodes",
		LoadReady:  line,
		Diagnostic: report,
	})
}

func setLimits(duration time.Duration) error {
	cpuSeconds := uint64((duration+time.Second-1)/time.Second) + 2
	for _, item := range []struct {
		resource int
		value    uint64
	}{
		{syscall.RLIMIT_AS, 2 * 1024 * mib},
		{syscall.RLIMIT_CPU, cpuSeconds},
		{syscall.RLIMIT_FSIZE, 8 * mib},
		{syscall.RLIMIT_NOFILE, 32},
		{syscall.RLIMIT_CORE, 0},
	} {
		if err := syscall.Setrlimit(item.resource, &syscall.Rlimit{Cur: item.value, Max: item.value}); err != nil {
			return fmt.Errorf("resource limit %d: %w", item.resource, err)
		}
	}
	runtime.GOMAXPROCS(1)
	debug.SetMemoryLimit(96 * mib) // GC target only; RLIMIT_AS above is the hard limit.
	return nil
}

func load(ctx context.Context, name, path string, duration time.Duration) error {
	if !knownScenario(name) {
		return fmt.Errorf("unknown load")
	}
	if err := validDuration(duration); err != nil {
		return err
	}
	if err := setLimits(duration); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, duration+time.Second)
	defer cancel()
	var held [][]byte
	allocate := func(size int) {
		b := make([]byte, size)
		for i := 0; i < len(b); i += os.Getpagesize() {
			b[i] = 1
		}
		held = append(held, b)
	}
	if name == "ambiguous" {
		allocate(64 * mib) // A stable resident working set, not ongoing growth.
	}
	var file *os.File
	if name == "io" {
		var fs syscall.Statfs_t
		if err := syscall.Statfs(path, &fs); err != nil {
			return err
		}
		if fs.Bavail*uint64(fs.Bsize) < 64*mib {
			return fmt.Errorf("I/O experiment requires at least 64 MiB available; writes remain capped at 8 MiB")
		}
		var err error
		file, err = os.OpenFile(filepath.Join(path, "io.bin"), os.O_CREATE|os.O_EXCL|os.O_RDWR, 0600)
		if err != nil {
			return err
		}
		defer file.Close()
		// Unlink immediately so even SIGKILL frees its bounded disk allocation.
		if err := os.Remove(file.Name()); err != nil {
			return err
		}
	}
	setup := "ready"
	if name == "disk-full" {
		if err := fillTinyFilesystem(path); err != nil {
			return err
		}
		setup = "ready; harness-only write probe returned ENOSPC on verified 8 MiB tmpfs"
	}
	fmt.Println(setup)
	start := time.Now()
	buffer := make([]byte, mib)
	for i := range buffer {
		buffer[i] = byte(i)
	}
	var offset int64
	allocations := 0
	for ctx.Err() == nil {
		elapsed := time.Since(start)
		switch name {
		case "cpu":
			spin(10 * time.Millisecond)
		case "ambiguous":
			if elapsed < duration/2 {
				spin(10 * time.Millisecond)
			} else {
				pause(ctx, 20*time.Millisecond)
			}
		case "memory":
			if allocations < 8 && elapsed >= time.Duration(allocations+1)*duration/10 {
				allocate(8 * mib)
				allocations++
			}
			pause(ctx, 10*time.Millisecond)
		case "io":
			if _, err := file.WriteAt(buffer, offset); err != nil {
				return err
			}
			if err := file.Sync(); err != nil {
				return err
			}
			offset = (offset + mib) % (8 * mib)
			pause(ctx, time.Second/16) // Not catch-up pacing: at most 16 MiB/s after the first write.
		default:
			pause(ctx, 20*time.Millisecond)
		}
	}
	runtime.KeepAlive(held)
	return nil
}

func pause(ctx context.Context, duration time.Duration) {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-timer.C:
	case <-ctx.Done():
	}
}

func spin(duration time.Duration) {
	end := time.Now().Add(duration)
	for time.Now().Before(end) {
	}
}

func fillTinyFilesystem(path string) error {
	var fs syscall.Statfs_t
	if err := syscall.Statfs(path, &fs); err != nil {
		return err
	}
	if fs.Type != tmpfsMagic || fs.Blocks*uint64(fs.Bsize) != 8*mib || fs.Files != 64 {
		return fmt.Errorf("refusing disk-full: expected isolated 8 MiB/64-inode tmpfs")
	}
	file, err := os.OpenFile(filepath.Join(path, "full.bin"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	defer file.Close()
	buffer := make([]byte, mib)
	for i := 0; i < 8; i++ {
		if _, err := file.Write(buffer); err != nil {
			return fmt.Errorf("filling bounded tmpfs: %w", err)
		}
	}
	probe, err := os.OpenFile(filepath.Join(path, "probe.bin"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	defer probe.Close()
	_, err = probe.Write([]byte{1}) // A new file avoids confusing RLIMIT_FSIZE with ENOSPC.
	if !errors.Is(err, syscall.ENOSPC) {
		return fmt.Errorf("expected ENOSPC on tiny tmpfs, got %v", err)
	}
	return nil
}
