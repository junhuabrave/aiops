package main

import "fmt"

func assess(r *Report) {
	r.Metrics = map[string]float64{}
	r.Findings = []Finding{}
	r.Limitations = []string{
		"Deterministic symptom thresholds, not root cause, workload health, leak detection or production readiness; no model/provider calls.",
		"Five non-atomic snapshots over a short window may miss bursts. Process CPU percent uses one logical CPU as 100%; host percent uses all visible CPUs.",
		"Host /proc counters and PSI may cover a wider scope than this process. Cgroup quotas, throttling, memory limits/events, swap activity and OOM history are not collected; host headroom cannot rule out cgroup exhaustion.",
		"RSS is resident memory, not ownership/leak proof. Filesystem capacity is not disk I/O utilization; statfs does not probe writes, quotas, read-only mounts or application errors.",
		"Process write_bytes is kernel-accounted storage traffic, not measured physical throughput or device attribution; fsync latency and application latency are not collected.",
	}
	if r.Device == "" {
		r.Limitations = append(r.Limitations, "No block device selected or directly mapped; disk utilization/saturation is unassessed. Overlay and tmpfs paths cannot safely be attributed to a physical device here.")
	} else {
		r.Limitations = append(r.Limitations, "Device scope: "+r.DeviceSource+". Device counters include other processes. Busy time is not a saturation/latency proof on parallel devices.")
	}
	add := func(code, evidence, assessment string, next ...string) {
		r.Findings = append(r.Findings, Finding{Code: code, Evidence: []string{evidence}, Assessment: assessment, NextChecks: next})
	}
	if len(r.Samples) < 2 || r.WindowSeconds <= 0 || r.ClockTicks == 0 {
		add("visibility_limited", "Missing observation window or clock frequency", "Cannot compute trustworthy rates.", "Collect a complete observation window.")
		return
	}
	first, last := r.Samples[0], r.Samples[len(r.Samples)-1]
	processOK, cpuOK, diskOK, fsOK, memoryOK := true, true, r.Device != "", true, true
	visibility := []string{}
	for i, s := range r.Samples {
		for _, err := range s.Errors {
			visibility = append(visibility, fmt.Sprintf("samples[%d]: %s", i, err))
		}
		processOK = processOK && s.Process != nil && first.Process != nil && s.Process.StartTicks == first.Process.StartTicks
		cpuOK = cpuOK && s.CPU != nil
		diskOK = diskOK && s.Disk != nil
		fsOK = fsOK && s.Filesystem != nil && first.Filesystem != nil && s.Filesystem.DeviceID == first.Filesystem.DeviceID && s.Filesystem.Type == first.Filesystem.Type && s.Filesystem.Bytes == first.Filesystem.Bytes
		memoryOK = memoryOK && s.Memory != nil
		if i > 0 {
			p := r.Samples[i-1]
			if s.At.Sub(p.At).Seconds() <= 0 {
				add("visibility_limited", "Non-increasing sample timestamps", "Cannot compute trustworthy rates.", "Repeat collection.")
				return
			}
			if processOK && s.Process.CPUTicks < p.Process.CPUTicks {
				processOK = false
			}
			if cpuOK && (s.CPU.Total <= p.CPU.Total || s.CPU.Idle < p.CPU.Idle || s.CPU.IOWait < p.CPU.IOWait || s.CPU.Total-p.CPU.Total < (s.CPU.Idle-p.CPU.Idle)+(s.CPU.IOWait-p.CPU.IOWait)) {
				cpuOK = false
			}
			if diskOK && (s.Disk.BusyMillis < p.Disk.BusyMillis || s.Disk.ReadSectors < p.Disk.ReadSectors || s.Disk.WriteSectors < p.Disk.WriteSectors) {
				diskOK = false
			}
		}
	}
	for _, kind := range []string{"cpu", "memory", "io"} {
		valid := true
		for i, s := range r.Samples {
			v, ok := s.Pressure[kind]
			valid = valid && ok
			if i > 0 && v < r.Samples[i-1].Pressure[kind] {
				valid = false
			}
		}
		if valid {
			r.Metrics[kind+"_psi_some_percent"] = float64(last.Pressure[kind]-first.Pressure[kind]) / (r.WindowSeconds * 1e4)
		} else {
			visibility = append(visibility, kind+" PSI unavailable or reset")
		}
	}
	if processOK {
		r.Metrics["process_cpu_percent_one_core"] = float64(last.Process.CPUTicks-first.Process.CPUTicks) / float64(r.ClockTicks) / r.WindowSeconds * 100
		r.Metrics["rss_start_bytes"] = float64(first.Process.RSSBytes)
		r.Metrics["rss_end_bytes"] = float64(last.Process.RSSBytes)
		growth := float64(last.Process.RSSBytes) - float64(first.Process.RSSBytes)
		r.Metrics["rss_growth_bytes"] = growth
		minCPU, maxCPU := 1e9, 0.0
		for i := 1; i < len(r.Samples); i++ {
			p, s := r.Samples[i-1], r.Samples[i]
			v := float64(s.Process.CPUTicks-p.Process.CPUTicks) / float64(r.ClockTicks) / s.At.Sub(p.At).Seconds() * 100
			minCPU, maxCPU = min(minCPU, v), max(maxCPU, v)
		}
		r.Metrics["process_cpu_interval_min_percent"] = minCPU
		r.Metrics["process_cpu_interval_max_percent"] = maxCPU
		if r.Metrics["process_cpu_percent_one_core"] >= 60 {
			add("process_cpu_busy", fmt.Sprintf("process CPU %.1f%% of one core across samples[0:%d]", r.Metrics["process_cpu_percent_one_core"], len(r.Samples)-1), "The selected process is CPU-active; this alone does not establish host saturation or a fault.", "Check workload throughput/latency and a CPU profile.", "Inspect CPU affinity, cgroup cpu.max/cpu.stat and host pressure.")
		}
		if maxCPU >= 60 && minCPU < 20 {
			add("variable_cpu_activity", fmt.Sprintf("process interval CPU ranges %.1f–%.1f%% of one core", minCPU, maxCPU), "The window contains both busy and quiet intervals; a single instantaneous observation would be ambiguous.", "Collect a longer window and correlate demand/latency before classifying a fault.")
		}
		if growth >= 16*1024*1024 {
			add("rss_growth", fmt.Sprintf("RSS grew %.1f MiB from %.1f to %.1f MiB", growth/(1<<20), float64(first.Process.RSSBytes)/(1<<20), float64(last.Process.RSSBytes)/(1<<20)), "Resident memory grew; neither a leak nor host memory exhaustion follows from this short window.", "Compare heap/allocator profiles over a longer window and workload size.", "Inspect memory.current, memory.max, memory.events, swap and OOM records in the process cgroup.")
		} else if last.Process.RSSBytes >= 48*1024*1024 {
			add("resident_memory_observed", fmt.Sprintf("RSS ends at %.1f MiB; net growth %.1f MiB", float64(last.Process.RSSBytes)/(1<<20), growth/(1<<20)), "A resident working set exceeds this lab's 48 MiB marker; this is not an application-specific high-memory threshold or exhaustion evidence.", "Compare the working set with the application's baseline, budget and cgroup limit.")
		}
		ioOK := true
		for i, s := range r.Samples {
			ioOK = ioOK && s.Process.WriteBytes != nil && s.Process.WriteCalls != nil
			if i > 0 && ioOK {
				p := r.Samples[i-1].Process
				ioOK = *s.Process.WriteBytes >= *p.WriteBytes && *s.Process.WriteCalls >= *p.WriteCalls
			}
		}
		if ioOK {
			r.Metrics["process_storage_write_bytes_per_second"] = float64(*last.Process.WriteBytes-*first.Process.WriteBytes) / r.WindowSeconds
			r.Metrics["process_write_syscalls_per_second"] = float64(*last.Process.WriteCalls-*first.Process.WriteCalls) / r.WindowSeconds
			if r.Metrics["process_storage_write_bytes_per_second"] >= 1<<20 {
				add("process_write_activity", fmt.Sprintf("kernel-accounted storage writes %.2f MiB/s; %.1f write syscalls/s", r.Metrics["process_storage_write_bytes_per_second"]/(1<<20), r.Metrics["process_write_syscalls_per_second"]), "The process generated storage write activity; capacity pressure and device saturation are separate questions.", "Map the filesystem to its block device; collect device busy time, queues and latency.", "Correlate with application latency and host/cgroup I/O pressure.")
			}
		} else {
			visibility = append(visibility, "process I/O counters unavailable or reset")
		}
	} else {
		visibility = append(visibility, "process missing, CPU counter reset or PID generation changed; process rates suppressed")
	}
	if cpuOK {
		total := float64(last.CPU.Total - first.CPU.Total)
		busy := 100 * (total - float64(last.CPU.Idle-first.CPU.Idle) - float64(last.CPU.IOWait-first.CPU.IOWait)) / total
		r.Metrics["host_cpu_busy_percent"] = busy
		r.Metrics["host_iowait_percent"] = float64(last.CPU.IOWait-first.CPU.IOWait) / total * 100
		if busy >= 90 {
			add("host_cpu_busy", fmt.Sprintf("host aggregate busy %.1f%%", busy), "Visible host CPU utilization is high; demand, cgroup limits and runnable pressure still need correlation before calling saturation.", "Inspect per-CPU utilization, runnable queues, steal time and CPU pressure for the affected scope.")
		}
	} else {
		visibility = append(visibility, "host CPU counters unavailable or reset")
	}
	if memoryOK {
		available := float64(last.Memory.Available) / float64(last.Memory.Total) * 100
		r.Metrics["host_memory_available_percent"] = available
		if available < 10 {
			add("host_memory_headroom_low", fmt.Sprintf("MemAvailable %.1f%% of MemTotal in final sample", available), "Host memory headroom is low; this is not proof of exhaustion or attribution to the selected process.", "Check memory PSI, swap-in/out, reclaim, OOM events and cgroup limits.")
		}
	} else {
		visibility = append(visibility, "host memory unavailable")
	}
	if fsOK && last.Filesystem.Bytes > 0 {
		fs := last.Filesystem
		r.Metrics["filesystem_available_percent"] = 100 * float64(fs.AvailableBytes) / float64(fs.Bytes)
		r.Metrics["filesystem_used_percent"] = 100 * (1 - float64(fs.FreeBytes)/float64(fs.Bytes))
		if fs.AvailableBytes == 0 {
			add("filesystem_no_available_space", fmt.Sprintf("final statfs: available=%d, free=%d, total=%d bytes", fs.AvailableBytes, fs.FreeBytes, fs.Bytes), "No space is available to an unprivileged writer on this filesystem. Reserved blocks, quotas and actual application errors need separate checks; the diagnostic did not attempt a write.", "Check the application's ENOSPC/EDQUOT errors, mount identity, reserved blocks and deleted-open files before cleanup.")
		} else if r.Metrics["filesystem_available_percent"] < 10 {
			add("filesystem_headroom_low", fmt.Sprintf("final statfs available %.2f%%", r.Metrics["filesystem_available_percent"]), "Filesystem capacity headroom is low, independently of I/O activity.", "Inspect growth rate, quotas and retention policy; do not delete data based on this report alone.")
		}
		if fs.Inodes > 0 && fs.FreeInodes == 0 {
			add("filesystem_no_free_inodes", "final statfs reports zero free inodes", "File creation may fail despite free byte capacity.", "Inspect inode usage and application errors.")
		}
	} else {
		visibility = append(visibility, "filesystem unavailable, changed or zero-sized")
	}
	if diskOK {
		busy := float64(last.Disk.BusyMillis-first.Disk.BusyMillis) / (r.WindowSeconds * 10)
		r.Metrics["selected_device_busy_percent"] = busy
		r.Metrics["selected_device_write_bytes_per_second"] = float64(last.Disk.WriteSectors-first.Disk.WriteSectors) * 512 / r.WindowSeconds
		if busy >= 80 {
			add("selected_device_busy", fmt.Sprintf("device %s busy time %.1f%%", r.Device, busy), "The selected device has sustained I/O activity; this does not prove saturation or attribution to this PID. See device_source for filesystem mapping.", "Confirm device mapping, queue depth and latency, including stacked/parallel device behavior.")
		}
	} else if r.Device != "" {
		visibility = append(visibility, "selected block device unavailable or counters reset")
	}
	if len(visibility) > 0 {
		r.Findings = append(r.Findings, Finding{Code: "visibility_limited", Evidence: visibility, Assessment: "Some evidence cannot support a conclusion; missing readings are not zero or healthy.", NextChecks: []string{"Resolve source permissions/scope and collect another complete window."}})
	}
	if len(r.Findings) == 0 {
		add("no_threshold_breach", "No configured symptom threshold crossed in available samples", "No selected symptom was detected; this is not proof of health. Device utilization remains unassessed unless a device is selected or mapped.", "Compare against application latency, baseline and cgroup limits; extend the window for intermittent symptoms.")
	}
}
