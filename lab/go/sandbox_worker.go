package main

import (
	"bufio"
	"bytes"
	"context"
	_ "embed"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"time"
)

//go:embed worker/specialist.py
var specialistScript []byte

// Linux amd64 only, like the native lab. Fail closed on other architectures.
// In addition to namespaces, deny sockets, process creation, namespace changes
// and host-inspection syscalls. No shell or project directory is mounted.
func sandboxFilter() []byte {
	var b bytes.Buffer
	add := func(code uint16, jt, jf uint8, k uint32) {
		binary.Write(&b, binary.LittleEndian, struct {
			Code   uint16
			JT, JF uint8
			K      uint32
		}{code, jt, jf, k})
	}
	add(0x20, 0, 0, 4)          // seccomp_data.arch
	add(0x15, 1, 0, 0xc000003e) // AUDIT_ARCH_X86_64
	add(0x06, 0, 0, 0x80000000) // KILL_PROCESS for a foreign ABI
	add(0x20, 0, 0, 0)          // syscall number
	add(0x35, 0, 1, 0x40000000) // also reject x32 ABI
	add(0x06, 0, 0, 0x80000000)
	// Also deny SysV allocations and io_uring, which could bypass the ordinary
	// allocation/socket paths, and host-global information/keyring APIs that
	// namespaces do not hide. This is defense in depth, not a VM boundary.
	for _, nr := range []uint32{29, 41, 53, 56, 57, 58, 64, 68, 99, 101, 103, 165, 248, 249, 250,
		272, 298, 308, 310, 311, 312, 321, 323, 425, 426, 427, 435} {
		add(0x15, 0, 1, nr)
		add(0x06, 0, 0, 0x00050001) // ERRNO EPERM
	}
	add(0x06, 0, 0, 0x7fff0000) // ALLOW remaining runtime syscalls
	return b.Bytes()
}

func isolatedCommand(ctx context.Context, script, filter string) (*exec.Cmd, *os.File, error) {
	if runtime.GOOS != "linux" || runtime.GOARCH != "amd64" {
		return nil, nil, errors.New("worker isolation requires Linux amd64")
	}
	f, err := os.Open(filter)
	if err != nil {
		return nil, nil, err
	}
	args := []string{"--as=268435456", "--cpu=5", "--nofile=32", "--fsize=1048576", "--core=0", "--",
		"/usr/bin/bwrap", "--unshare-all", "--unshare-user", "--disable-userns", "--die-with-parent", "--new-session",
		"--cap-drop", "ALL", "--clearenv", "--setenv", "HOME", "/tmp", "--setenv", "PATH", "/usr/bin",
		"--ro-bind", "/usr/bin/python3.13", "/usr/bin/python3.13",
		"--ro-bind", "/usr/lib/python3.13", "/usr/lib/python3.13",
		"--ro-bind", "/usr/lib/x86_64-linux-gnu", "/usr/lib/x86_64-linux-gnu",
		"--ro-bind", "/usr/lib64", "/usr/lib64", "--symlink", "usr/lib", "/lib", "--symlink", "usr/lib64", "/lib64",
		"--size", "1048576", "--tmpfs", "/tmp", "--chdir", "/tmp",
		"--ro-bind", script, "/worker.py", "--remount-ro", "/", "--seccomp", "3", "--", "/usr/bin/python3.13", "-I", "-B", "/worker.py"}
	cmd := exec.CommandContext(ctx, "/usr/bin/prlimit", args...)
	cmd.Env = []string{"PATH=/usr/bin:/bin"}
	cmd.ExtraFiles = []*os.File{f}
	cmd.WaitDelay = time.Second
	// Stderr is untrusted output, not an operator-facing diagnostic or log sink.
	cmd.Stderr = io.Discard
	return cmd, f, nil
}

func (s *taskStore) specialist(ctx context.Context, j sandboxRun, window frozenWindow, role, script, filter string) ([]evidencePoint, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	cmd, f, err := isolatedCommand(ctx, script, filter)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	input, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	defer input.Close()
	output, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	defer output.Close()
	if err = cmd.Start(); err != nil {
		return nil, err
	}
	waited := false
	defer func() {
		cancel()
		if !waited {
			cmd.Wait()
		}
	}()
	// Identity is the parent-owned pipe bound to this role/task/generation. The
	// child has no bearer token, DB socket, operator session or sibling channel.
	if err = s.sandboxPermit(ctx, j.ID, j.Generation, 0, 4096); err != nil {
		return nil, err
	}
	if err = json.NewEncoder(input).Encode(map[string]any{"role": role, "scope": j.Scope}); err != nil {
		return nil, err
	}
	scanner := bufio.NewScanner(output)
	scanner.Buffer(make([]byte, 4096), 16<<10)
	delivered := map[string]evidencePoint{}
	for scanner.Scan() {
		var request workerRequest
		if err = decodeExact(scanner.Bytes(), &request); err != nil {
			return nil, errors.New("worker protocol rejected")
		}
		if request.Kind == "finish" {
			points, e := sandboxSelection(request, delivered)
			if e != nil {
				return nil, e
			}
			if e = s.sandboxPermit(ctx, j.ID, j.Generation, len(scanner.Bytes()), 0); e != nil {
				return nil, e
			}
			input.Close()
			if scanner.Scan() || scanner.Err() != nil {
				return nil, errors.New("unexpected trailing worker output")
			}
			e = cmd.Wait()
			waited = true
			return points, e
		}
		result, e := sandboxTool(window, role, j.Scope, request)
		if e != nil {
			return nil, e
		}
		b, e := json.Marshal(result)
		if e != nil || len(b)+1 > 128<<10 {
			return nil, errors.New("tool result too large")
		}
		if e = s.sandboxPermit(ctx, j.ID, j.Generation, len(scanner.Bytes()), 128<<10); e != nil {
			return nil, e
		}
		if _, e = input.Write(append(b, '\n')); e != nil {
			return nil, e
		}
		for _, p := range result.Points {
			delivered[p.ID] = p
		}
	}
	return nil, errors.New("worker exited without a selection")
}

func (s *taskStore) executeSandbox(parent context.Context, j sandboxRun, approvedScript []byte) error {
	ctx, cancel := context.WithDeadline(parent, j.Deadline)
	defer cancel()
	// Polling stops a silent/hung worker as well as fencing its next message.
	stopped := make(chan struct{})
	defer func() { cancel(); <-stopped }()
	go func() {
		defer close(stopped)
		tick := time.NewTicker(time.Second)
		defer tick.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-tick.C:
				call, stop := context.WithTimeout(ctx, 2*time.Second)
				err := s.updateSandbox(call, j, "running", "Isolated scripted specialists running; no provider calls", nil)
				stop()
				if err != nil {
					cancel()
					return
				}
			}
		}
	}()
	job, err := s.get(ctx, j.Source)
	if err != nil {
		return err
	}
	exported, err := exportEvidence(job)
	if err != nil {
		return err
	}
	var window frozenWindow
	if err = decodeExact([]byte(exported.Payload), &window); err != nil {
		return err
	}
	dir, err := os.MkdirTemp("", "plant-specialist-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	script, filter := filepath.Join(dir, "worker.py"), filepath.Join(dir, "filter.bpf")
	if err = os.WriteFile(script, approvedScript, 0600); err != nil {
		return err
	}
	if err = os.WriteFile(filter, sandboxFilter(), 0600); err != nil {
		return err
	}
	report := &sandboxReport{Mode: "isolated_scripted_replay", Digest: exported.SHA256,
		Claims: map[string][]evidencePoint{}, Limitations: append(window.Limits,
			"Fixed three-role script, not model reasoning or dynamic delegation. No actions or provider calls.",
			"Scope restricts worker evidence, not viewer authorization. The demo has one broad operator.",
			"Read and byte reservations are durable; no token or currency spending is permitted.")}
	for _, role := range []string{"operations", "integration", "engineering"} {
		if err = s.updateSandbox(ctx, j, "running", "Starting isolated "+role+" replay", nil); err != nil {
			return err
		}
		points, e := s.specialist(ctx, j, window, role, script, filter)
		if e != nil {
			return e
		}
		report.Claims[role] = points
	}
	return s.updateSandbox(ctx, j, "completed", "Isolated replay completed; sampled observations, not RCA or recovery", report)
}

func (s *taskStore) sandboxWork(ctx context.Context, run string) {
	tick := time.NewTicker(500 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
		call, stop := context.WithTimeout(ctx, 3*time.Second)
		j, err := s.claimSandbox(call, run)
		stop()
		if err != nil {
			if ctx.Err() == nil {
				log.Printf("sandbox scheduler unavailable: %v", err)
			}
			continue
		}
		if j.ID == "" {
			continue
		}
		if err = s.executeSandbox(ctx, j, specialistScript); err != nil {
			// Never reflect worker output or unvalidated partial results to users.
			call, stop = context.WithTimeout(ctx, 3*time.Second)
			e := s.updateSandbox(call, j, "blocked", "Worker isolation, protocol, budget or storage check failed; no result published", nil)
			stop()
			if e != nil && !errors.Is(e, errFenced) && ctx.Err() == nil {
				log.Printf("sandbox completion unavailable: %v", e)
			}
		}
	}
}
