package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// Opt-in: real bounded loads and namespaces, not mocked OS evidence.
func TestHarnessCLI(t *testing.T) {
	if os.Getenv("RESOURCE_LAB_E2E") != "1" {
		t.Skip("set RESOURCE_LAB_E2E=1 to run real isolated loads")
	}
	bin := filepath.Join(t.TempDir(), "resource-lab")
	if out, err := exec.Command("go", "build", "-o", bin, ".").CombinedOutput(); err != nil {
		t.Fatalf("build: %v: %s", err, out)
	}
	scratch := t.TempDir()
	command := func(args ...string) *exec.Cmd {
		cmd := exec.Command(bin, args...)
		cmd.Env = append(os.Environ(), "TMPDIR="+scratch)
		return cmd
	}
	t.Run("all scenarios", func(t *testing.T) {
		out, err := command("run", "--case", "all", "--duration", "3s").CombinedOutput()
		if err != nil {
			t.Fatalf("run: %v: %s", err, out)
		}
		var experiments []Experiment
		if err := json.Unmarshal(out, &experiments); err != nil {
			t.Fatal(err)
		}
		if len(experiments) != 6 {
			t.Fatal(len(experiments))
		}
		for _, e := range experiments {
			r := e.Diagnostic
			if len(r.Samples) != 5 || r.WindowSeconds < 2.9 || r.WindowSeconds > 5 {
				t.Fatalf("window: %+v", r)
			}
			want := map[string]string{"cpu": "process_cpu_busy", "memory": "rss_growth", "io": "process_write_activity", "disk-full": "filesystem_no_available_space", "ambiguous": "variable_cpu_activity"}[e.Scenario]
			if want != "" && !slices.Contains(codes(r), want) {
				t.Fatalf("%s: stimulus not observed: %v", e.Scenario, codes(r))
			}
			if e.Scenario == "healthy" && (slices.Contains(codes(r), "process_cpu_busy") || slices.Contains(codes(r), "rss_growth")) {
				t.Fatal(codes(r))
			}
			if e.Scenario == "ambiguous" && (!slices.Contains(codes(r), "resident_memory_observed") || slices.Contains(codes(r), "rss_growth")) {
				t.Fatal(codes(r))
			}
			if e.Scenario == "disk-full" {
				fs := r.Samples[0].Filesystem
				if fs.Type != tmpfsMagic || fs.Bytes != 8*mib || fs.AvailableBytes != 0 || !strings.Contains(e.LoadReady, "ENOSPC") {
					t.Fatalf("not isolated full tmpfs: %+v", e)
				}
			}
			if _, err := os.Stat(r.Path); !os.IsNotExist(err) {
				t.Fatalf("scratch leaked: %s: %v", r.Path, err)
			}
			busy := "unavailable"
			if value, ok := r.Metrics["selected_device_busy_percent"]; ok {
				busy = fmt.Sprintf("%.2f%%", value)
			}
			t.Logf("%s: codes=%v cpu=%.1f%% host=%.1f%% RSS delta=%.1f MiB writes=%.2f MiB/s device=%q busy=%s", e.Scenario, codes(r), r.Metrics["process_cpu_percent_one_core"], r.Metrics["host_cpu_busy_percent"], r.Metrics["rss_growth_bytes"]/mib, r.Metrics["process_storage_write_bytes_per_second"]/mib, r.Device, busy)
		}
	})
	t.Run("limits cancellation and parent death", func(t *testing.T) {
		for _, signal := range []syscall.Signal{syscall.SIGTERM, syscall.SIGKILL} {
			cmd := command("run", "--case", "io", "--duration", "8s")
			var stderr bytes.Buffer
			cmd.Stderr = &stderr
			if err := cmd.Start(); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { cmd.Process.Kill() })
			// Traverse only this command's descendants, not unrelated workloads.
			pids := waitForDescendants(t, cmd.Process.Pid, 3)
			worker := pids[len(pids)-1]
			limits, err := os.ReadFile(fmt.Sprintf("/proc/%d/limits", worker))
			if err != nil {
				t.Fatal(err)
			}
			for label, expected := range map[string]string{"Max address space": "2147483648", "Max file size": "8388608", "Max cpu time": "10", "Max open files": "32", "Max core file size": "0"} {
				found := false
				for _, line := range strings.Split(string(limits), "\n") {
					if strings.HasPrefix(line, label) {
						fields := strings.Fields(strings.TrimPrefix(line, label))
						found = len(fields) >= 2 && fields[0] == expected && fields[1] == expected
					}
				}
				if !found {
					t.Fatalf("missing hard limit %s=%s: %s", label, expected, limits)
				}
			}
			// A concurrent invocation must fail rather than multiply the load budget.
			if out, err := command("run", "--case", "cpu").CombinedOutput(); err == nil || !strings.Contains(string(out), "another resource harness") {
				t.Fatalf("concurrent run not blocked: %v %s", err, out)
			}
			started := time.Now()
			if err := cmd.Process.Signal(signal); err != nil {
				t.Fatal(err)
			}
			if err := cmd.Wait(); err == nil {
				t.Fatal("cancelled run reported success")
			}
			for _, pid := range pids {
				for time.Since(started) < 2*time.Second {
					data, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
					if os.IsNotExist(err) || (err == nil && strings.Fields(string(data)[strings.LastIndex(string(data), ")")+1:])[0] == "Z") {
						break
					}
					time.Sleep(20 * time.Millisecond)
				}
				if time.Since(started) >= 2*time.Second {
					t.Fatalf("descendant %d survived %s", pid, signal)
				}
			}
			entries, err := os.ReadDir(scratch)
			if err != nil {
				t.Fatal(err)
			}
			for _, entry := range entries {
				if !entry.IsDir() {
					continue
				} // Persistent advisory lock file.
				children, err := os.ReadDir(filepath.Join(scratch, entry.Name()))
				if err != nil || len(children) > 0 {
					t.Fatalf("payload leaked: %v %v", children, err)
				}
				if signal == syscall.SIGTERM {
					t.Fatal("normal cancellation leaked a directory")
				}
			}
			t.Logf("%s: hard limits verified, concurrency refused, descendants stopped in %s, no payload files", signal, time.Since(started))
		}
	})
	t.Run("outer deadline stops frozen coordinator", func(t *testing.T) {
		cmd := command("run", "--case", "cpu", "--duration", "3s")
		started := time.Now()
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { cmd.Process.Kill() })
		pids := waitForDescendants(t, cmd.Process.Pid, 3)
		if err := syscall.Kill(pids[1], syscall.SIGSTOP); err != nil {
			t.Fatal(err)
		}
		if err := cmd.Wait(); err == nil {
			t.Fatal("frozen coordinator reported success")
		}
		if elapsed := time.Since(started); elapsed < 8*time.Second || elapsed > 10*time.Second {
			t.Fatalf("outer deadline: %s", elapsed)
		}
		if data, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pids[1])); err == nil && !strings.Contains(string(data), ") Z ") {
			t.Fatal("frozen coordinator survived", string(data))
		}
		t.Logf("outer wall deadline killed frozen coordinator after %s", time.Since(started))
	})
	t.Run("read only missing scope and unsafe load refusal", func(t *testing.T) {
		out, err := command("diagnose", "--pid", "2147483647", "--path", filepath.Join(scratch, "does-not-exist"), "--duration", "3s").CombinedOutput()
		if err != nil {
			t.Fatalf("diagnose: %v %s", err, out)
		}
		var r Report
		if err := json.Unmarshal(out, &r); err != nil || !slices.Contains(codes(r), "visibility_limited") {
			t.Fatalf("missing scope: %v %s", err, out)
		}
		if _, err := os.Stat(r.Path); !os.IsNotExist(err) {
			t.Fatal("diagnostic created its target")
		}
		if out, err := command("_load", "disk-full", scratch, "3s", "1").CombinedOutput(); err == nil || !strings.Contains(string(out), "isolated PID-1") {
			t.Fatalf("unsafe load allowed: %v %s", err, out)
		}
		if err := fillTinyFilesystem(scratch); err == nil {
			t.Fatal("disk-full accepted ordinary filesystem")
		}
	})
}

func waitForDescendants(t *testing.T, parent, count int) []int {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		var pids []int
		var visit func(int)
		visit = func(pid int) {
			// Go may fork from any OS thread, so inspect every task's children.
			paths, _ := filepath.Glob(fmt.Sprintf("/proc/%d/task/*/children", pid))
			for _, path := range paths {
				data, _ := os.ReadFile(path)
				for _, field := range strings.Fields(string(data)) {
					child, _ := strconv.Atoi(field)
					pids = append(pids, child)
					visit(child)
				}
			}
		}
		visit(parent)
		if len(pids) >= count {
			time.Sleep(100 * time.Millisecond)
			return pids[:count]
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("load descendants did not start")
	return nil
}
