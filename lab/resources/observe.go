package main

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

type Process struct {
	StartTicks uint64  `json:"start_ticks"`
	CPUTicks   uint64  `json:"cpu_ticks"`
	RSSBytes   uint64  `json:"rss_bytes"`
	WriteBytes *uint64 `json:"storage_write_bytes"`
	WriteCalls *uint64 `json:"write_syscalls"`
}

type HostCPU struct {
	Total  uint64 `json:"total_ticks"`
	Idle   uint64 `json:"idle_ticks"`
	IOWait uint64 `json:"iowait_ticks"`
}

type Memory struct {
	Total     uint64 `json:"total_bytes"`
	Available uint64 `json:"available_bytes"`
}

type Filesystem struct {
	DeviceID       uint64 `json:"device_id"`
	Type           int64  `json:"type_magic"`
	Bytes          uint64 `json:"total_bytes"`
	FreeBytes      uint64 `json:"free_bytes"`
	AvailableBytes uint64 `json:"available_bytes"`
	Inodes         uint64 `json:"inodes"`
	FreeInodes     uint64 `json:"free_inodes"`
}

type Disk struct {
	ReadSectors  uint64 `json:"read_sectors_512_bytes"`
	WriteSectors uint64 `json:"write_sectors_512_bytes"`
	BusyMillis   uint64 `json:"io_busy_milliseconds"`
}

type Sample struct {
	At         time.Time         `json:"at"`
	Process    *Process          `json:"process"`
	CPU        *HostCPU          `json:"host_cpu"`
	Memory     *Memory           `json:"host_memory"`
	Filesystem *Filesystem       `json:"filesystem"`
	Pressure   map[string]uint64 `json:"psi_some_total_microseconds"`
	Disk       *Disk             `json:"block_device"`
	Errors     []string          `json:"errors"`
}

type Finding struct {
	Code       string   `json:"code"`
	Evidence   []string `json:"evidence"`
	Assessment string   `json:"assessment"`
	NextChecks []string `json:"next_checks"`
}

type Report struct {
	SchemaVersion int                `json:"schema_version"`
	Mode          string             `json:"mode"`
	PID           int                `json:"pid"`
	Path          string             `json:"path"`
	Device        string             `json:"device"`
	DeviceSource  string             `json:"device_source"`
	ClockTicks    uint64             `json:"clock_ticks_per_second"`
	WindowSeconds float64            `json:"window_seconds"`
	Samples       []Sample           `json:"samples"`
	Metrics       map[string]float64 `json:"derived"`
	Findings      []Finding          `json:"findings"`
	Limitations   []string           `json:"limitations"`
}

// No harness scenario, control file, environment label, command line or process
// name enters this collection/assessment boundary. It also works on arbitrary PIDs.
func observe(ctx context.Context, pid int, path, device string, duration time.Duration) (Report, error) {
	report := Report{SchemaVersion: 1, Mode: "read_only_deterministic", PID: pid, Device: device}
	var err error
	report.Path, err = filepath.Abs(path)
	if err != nil {
		return report, err
	}
	if device != "" && (strings.ContainsAny(device, "/ \t\n") || device == "." || device == "..") {
		return report, fmt.Errorf("device must be a single block device name")
	}
	if device != "" {
		report.DeviceSource = "explicit selection; association with PID/path unverified"
	} else if info, err := os.Stat(path); err == nil {
		dev := info.Sys().(*syscall.Stat_t).Dev
		major := (dev>>8)&0xfff | (dev>>32)&0xfffff000
		minor := dev&0xff | (dev>>12)&0xffffff00
		if target, err := filepath.EvalSymlinks(fmt.Sprintf("/sys/dev/block/%d:%d", major, minor)); err == nil {
			device = filepath.Base(target)
			report.Device = device
			report.DeviceSource = fmt.Sprintf("filesystem st_dev %d:%d via /sys/dev/block; not exclusive PID attribution", major, minor)
		}
	}
	clockCtx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	out, err := exec.CommandContext(clockCtx, "getconf", "CLK_TCK").Output()
	if err != nil {
		return report, fmt.Errorf("getconf CLK_TCK: %w", err)
	}
	report.ClockTicks, err = strconv.ParseUint(strings.TrimSpace(string(out)), 10, 64)
	if err != nil || report.ClockTicks == 0 {
		return report, fmt.Errorf("invalid CLK_TCK %q", out)
	}
	start := time.Now()
	for i := 0; i <= 4; i++ {
		if i > 0 {
			timer := time.NewTimer(time.Until(start.Add(time.Duration(i) * duration / 4)))
			select {
			case <-ctx.Done():
				timer.Stop()
				return report, ctx.Err()
			case <-timer.C:
			}
		}
		report.Samples = append(report.Samples, snapshot(pid, path, device))
	}
	report.WindowSeconds = report.Samples[4].At.Sub(report.Samples[0].At).Seconds()
	assess(&report)
	return report, nil
}

func snapshot(pid int, path, device string) Sample {
	s := Sample{At: time.Now().UTC(), Pressure: map[string]uint64{}, Errors: []string{}}
	record := func(source string, err error) {
		if err != nil {
			s.Errors = append(s.Errors, source+": "+err.Error())
		}
	}
	stat, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err == nil {
		s.Process, err = parseProcess(string(stat))
	}
	record("process stat", err)
	if s.Process != nil {
		values, ioErr := keyValues(fmt.Sprintf("/proc/%d/io", pid))
		record("process io", ioErr)
		if ioErr == nil {
			if v, ok := values["write_bytes"]; ok {
				s.Process.WriteBytes = &v
			}
			if v, ok := values["syscw"]; ok {
				s.Process.WriteCalls = &v
			}
		}
	}
	cpu, err := os.ReadFile("/proc/stat")
	if err == nil {
		s.CPU, err = parseCPU(strings.SplitN(string(cpu), "\n", 2)[0])
	}
	record("host cpu", err)
	mem, err := keyValues("/proc/meminfo")
	if err == nil {
		total, haveTotal := mem["MemTotal"]
		available, haveAvailable := mem["MemAvailable"]
		if !haveTotal || !haveAvailable || total == 0 || available > total {
			err = fmt.Errorf("missing or invalid MemTotal/MemAvailable")
		} else {
			s.Memory = &Memory{Total: total * 1024, Available: available * 1024}
		}
	}
	record("host memory", err)
	var fs syscall.Statfs_t
	err = syscall.Statfs(path, &fs)
	if err == nil {
		var info os.FileInfo
		info, err = os.Stat(path)
		if err == nil {
			s.Filesystem = &Filesystem{DeviceID: info.Sys().(*syscall.Stat_t).Dev, Type: fs.Type, Bytes: fs.Blocks * uint64(fs.Bsize), FreeBytes: fs.Bfree * uint64(fs.Bsize), AvailableBytes: fs.Bavail * uint64(fs.Bsize), Inodes: fs.Files, FreeInodes: fs.Ffree}
		}
	}
	record("filesystem", err)
	for _, kind := range []string{"cpu", "memory", "io"} {
		data, err := os.ReadFile("/proc/pressure/" + kind)
		if err == nil {
			var value uint64
			value, err = parsePressure(string(data))
			if err == nil {
				s.Pressure[kind] = value
			}
		}
		record("psi "+kind, err)
	}
	if device != "" {
		s.Disk, err = readDisk(device)
		record("block device", err)
	}
	return s
}

func parseProcess(line string) (*Process, error) {
	// comm may contain spaces and parentheses; fields after its final ')' are fixed.
	end := strings.LastIndex(line, ")")
	if end < 0 {
		return nil, fmt.Errorf("malformed process stat")
	}
	f := strings.Fields(line[end+1:])
	if len(f) < 22 {
		return nil, fmt.Errorf("short process stat")
	}
	if f[0] == "Z" || f[0] == "X" {
		return nil, fmt.Errorf("process has exited")
	}
	values, err := numbers([]string{f[11], f[12], f[19], f[21]})
	if err != nil {
		return nil, err
	}
	return &Process{StartTicks: values[2], CPUTicks: values[0] + values[1], RSSBytes: values[3] * uint64(os.Getpagesize())}, nil
}

func parseCPU(line string) (*HostCPU, error) {
	f := strings.Fields(line)
	if len(f) < 9 || f[0] != "cpu" {
		return nil, fmt.Errorf("missing aggregate CPU counters")
	}
	v, err := numbers(f[1:9]) // guest/guest_nice already included in user/nice.
	if err != nil {
		return nil, err
	}
	cpu := &HostCPU{Idle: v[3], IOWait: v[4]}
	for _, value := range v {
		cpu.Total += value
	}
	return cpu, nil
}

func numbers(fields []string) ([]uint64, error) {
	values := make([]uint64, len(fields))
	for i, f := range fields {
		v, err := strconv.ParseUint(f, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("invalid counter %q", f)
		}
		values[i] = v
	}
	return values, nil
}

func keyValues(path string) (map[string]uint64, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	values := map[string]uint64{}
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		f := strings.Fields(scanner.Text())
		if len(f) < 2 {
			continue
		}
		v, err := strconv.ParseUint(f[1], 10, 64)
		if err != nil {
			return nil, err
		}
		values[strings.TrimSuffix(f[0], ":")] = v
	}
	return values, scanner.Err()
}

func parsePressure(data string) (uint64, error) {
	for _, line := range strings.Split(data, "\n") {
		f := strings.Fields(line)
		if len(f) == 0 || f[0] != "some" {
			continue
		}
		for _, field := range f[1:] {
			if strings.HasPrefix(field, "total=") {
				return strconv.ParseUint(strings.TrimPrefix(field, "total="), 10, 64)
			}
		}
	}
	return 0, fmt.Errorf("missing PSI some total")
}

func readDisk(device string) (*Disk, error) {
	data, err := os.ReadFile("/proc/diskstats")
	if err != nil {
		return nil, err
	}
	for _, line := range strings.Split(string(data), "\n") {
		f := strings.Fields(line)
		if len(f) >= 14 && f[2] == device {
			v, err := numbers([]string{f[5], f[9], f[12]})
			if err != nil {
				return nil, err
			}
			return &Disk{ReadSectors: v[0], WriteSectors: v[1], BusyMillis: v[2]}, nil
		}
	}
	return nil, fmt.Errorf("device %q not visible", device)
}
