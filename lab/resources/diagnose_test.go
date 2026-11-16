package main

import (
	"encoding/json"
	"math"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"
)

func baseline() Report {
	r := Report{ClockTicks: 100, WindowSeconds: 4, Device: "test-device", DeviceSource: "test fixture"}
	for i := 0; i < 5; i++ {
		zero := uint64(0)
		r.Samples = append(r.Samples, Sample{
			At:         time.Unix(100+int64(i), 0),
			Process:    &Process{StartTicks: 17, CPUTicks: 100, RSSBytes: 4 * mib, WriteBytes: &zero, WriteCalls: &zero},
			CPU:        &HostCPU{Total: 10000 + uint64(i)*400, Idle: 5000 + uint64(i)*300},
			Memory:     &Memory{Total: 1024 * mib, Available: 600 * mib},
			Filesystem: &Filesystem{DeviceID: 1, Bytes: 100 * mib, FreeBytes: 70 * mib, AvailableBytes: 60 * mib, Inodes: 100, FreeInodes: 50},
			Pressure:   map[string]uint64{"cpu": 0, "memory": 0, "io": 0},
			Disk:       &Disk{},
		})
	}
	return r
}

func codes(r Report) []string {
	var out []string
	for _, f := range r.Findings {
		out = append(out, f.Code)
	}
	return out
}

func TestReportDistinguishesResourceScopes(t *testing.T) {
	cases := []struct {
		name   string
		change func(*Report)
		want   []string
	}{
		{"healthy", func(r *Report) {}, []string{"no_threshold_breach"}},
		{"one core busy not host saturation", func(r *Report) {
			for i := range r.Samples {
				r.Samples[i].Process.CPUTicks += uint64(i) * 100
			}
		}, []string{"process_cpu_busy"}},
		{"host busy not target process", func(r *Report) {
			for i := range r.Samples {
				r.Samples[i].CPU.Idle = 5000 + uint64(i)*20
			}
		}, []string{"host_cpu_busy"}},
		{"RSS growth with ample headroom", func(r *Report) {
			for i := range r.Samples {
				r.Samples[i].Process.RSSBytes += uint64(i) * 8 * mib
			}
		}, []string{"rss_growth"}},
		{"large stable RSS is not growth", func(r *Report) {
			for i := range r.Samples {
				r.Samples[i].Process.RSSBytes = 64 * mib
			}
		}, []string{"resident_memory_observed"}},
		{"low host memory not target growth", func(r *Report) {
			for i := range r.Samples {
				r.Samples[i].Memory.Available = 8 * mib
			}
		}, []string{"host_memory_headroom_low"}},
		{"writes with ample capacity", func(r *Report) {
			for i := range r.Samples {
				b, c := uint64(i)*3*mib, uint64(i)*4
				r.Samples[i].Process.WriteBytes, r.Samples[i].Process.WriteCalls = &b, &c
			}
		}, []string{"process_write_activity"}},
		{"full capacity with no writes", func(r *Report) {
			for i := range r.Samples {
				r.Samples[i].Filesystem.FreeBytes, r.Samples[i].Filesystem.AvailableBytes = 0, 0
			}
		}, []string{"filesystem_no_available_space"}},
		{"reserved free capacity", func(r *Report) {
			for i := range r.Samples {
				r.Samples[i].Filesystem.AvailableBytes = 0
			}
		}, []string{"filesystem_no_available_space"}},
		{"inode exhaustion not byte capacity", func(r *Report) {
			for i := range r.Samples {
				r.Samples[i].Filesystem.FreeInodes = 0
			}
		}, []string{"filesystem_no_free_inodes"}},
		{"device activity not capacity", func(r *Report) {
			for i := range r.Samples {
				r.Samples[i].Disk.BusyMillis = uint64(i) * 900
			}
		}, []string{"selected_device_busy"}},
		{"busy then quiet", func(r *Report) {
			for i, v := range []uint64{100, 200, 300, 300, 300} {
				r.Samples[i].Process.CPUTicks = v
			}
		}, []string{"variable_cpu_activity"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := baseline()
			tc.change(&r)
			assess(&r)
			if !reflect.DeepEqual(codes(r), tc.want) {
				t.Fatalf("got %v want %v", codes(r), tc.want)
			}
			if _, err := json.Marshal(r); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestMissingResetAndChangedEvidenceNeverBecomesHealthy(t *testing.T) {
	for _, kind := range []string{"missing process", "middle generation", "counter reset", "missing io", "io reset", "missing host", "iowait reset", "missing memory", "mount changed", "device reset", "missing psi"} {
		t.Run(kind, func(t *testing.T) {
			r := baseline()
			switch kind {
			case "missing process":
				r.Samples[2].Process = nil
			case "middle generation":
				r.Samples[2].Process.StartTicks++
			case "counter reset":
				r.Samples[2].Process.CPUTicks = 1
			case "missing io":
				r.Samples[2].Process.WriteBytes = nil
			case "io reset":
				v := uint64(9000000)
				r.Samples[2].Process.WriteBytes = &v
			case "missing host":
				r.Samples[2].CPU = nil
			case "iowait reset":
				r.Samples[2].CPU.IOWait = 1
			case "missing memory":
				r.Samples[2].Memory = nil
			case "mount changed":
				r.Samples[2].Filesystem.DeviceID++
			case "device reset":
				r.Samples[2].Disk.BusyMillis = 100
			case "missing psi":
				delete(r.Samples[2].Pressure, "cpu")
			}
			assess(&r)
			if !reflect.DeepEqual(codes(r), []string{"visibility_limited"}) {
				t.Fatal(codes(r))
			}
			if strings.Contains(kind, "process") || kind == "middle generation" || kind == "counter reset" {
				if _, ok := r.Metrics["process_cpu_percent_one_core"]; ok {
					t.Fatal("untrustworthy process rate retained")
				}
			}
		})
	}
}

func TestThresholdBoundariesAndUnits(t *testing.T) {
	for _, ticks := range []uint64{239, 240} {
		r := baseline()
		for i := range r.Samples {
			r.Samples[i].Process.CPUTicks += uint64(i) * ticks / 4
		}
		assess(&r)
		want := "no_threshold_breach"
		if ticks == 240 {
			want = "process_cpu_busy"
		}
		if !reflect.DeepEqual(codes(r), []string{want}) {
			t.Fatal(ticks, codes(r))
		}
		if r.Metrics["host_cpu_busy_percent"] != 25 {
			t.Fatal(r.Metrics)
		}
		if math.Abs(r.Metrics["process_cpu_percent_one_core"]-float64(ticks)/4) > 0.001 {
			t.Fatal(r.Metrics)
		}
	}
	for _, free := range []uint64{10 * mib, 10*mib - 1} {
		r := baseline()
		for i := range r.Samples {
			r.Samples[i].Filesystem.AvailableBytes = free
		}
		assess(&r)
		want := "no_threshold_breach"
		if free < 10*mib {
			want = "filesystem_headroom_low"
		}
		if !reflect.DeepEqual(codes(r), []string{want}) {
			t.Fatal(free, codes(r))
		}
	}
}

func TestProcAccounting(t *testing.T) {
	cpu, err := parseCPU("cpu  100 20 30 400 50 6 7 8 90 10")
	if err != nil || cpu.Total != 621 || cpu.Idle != 400 || cpu.IOWait != 50 {
		t.Fatal(cpu, err)
	}
	// Fields 14/15/22/24: utime=123, stime=45, generation=777, RSS=9 pages.
	p, err := parseProcess("42 (name with ) spaces) S 1 2 3 4 5 6 7 8 9 10 123 45 0 0 0 0 0 0 777 4096 9")
	if err != nil || p.CPUTicks != 168 || p.StartTicks != 777 || p.RSSBytes != uint64(9*os.Getpagesize()) {
		t.Fatal(p, err)
	}
}

func TestScenarioNamesCannotChangeAssessment(t *testing.T) {
	r := baseline()
	assess(&r)
	want := r.Findings
	for _, label := range scenarios {
		r.Path = "/tmp/" + label
		r.Device = label
		assess(&r)
		if !reflect.DeepEqual(r.Findings, want) {
			t.Fatal("assessment depended on a resource name")
		}
	}
}
