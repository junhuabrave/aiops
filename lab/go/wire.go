package main

import (
	"encoding/binary"
	"fmt"
	"hash/crc32"
	"io"
	"time"
)

const frameSize = 256

// The synthetic protocol is not compatible with any production messaging bus.
type frame struct {
	Run        string
	Feed       byte
	Generation uint64
	Sequence   uint64
	Source     int64
}

func encodeFrame(f frame) []byte {
	b := make([]byte, frameSize+4)
	binary.BigEndian.PutUint32(b, frameSize)
	b[4] = 1
	copy(b[5:21], f.Run)
	b[21] = f.Feed
	binary.BigEndian.PutUint64(b[22:30], f.Generation)
	binary.BigEndian.PutUint64(b[30:38], f.Sequence)
	binary.BigEndian.PutUint64(b[38:46], uint64(f.Source))
	for i := 46; i < len(b)-4; i++ {
		b[i] = byte(f.Sequence + uint64(i))
	}
	binary.BigEndian.PutUint32(b[len(b)-4:], crc32.ChecksumIEEE(b[4:len(b)-4]))
	return b
}

func readFrame(r io.Reader) (frame, []byte, error) {
	var prefix [4]byte
	if _, err := io.ReadFull(r, prefix[:]); err != nil {
		return frame{}, nil, err
	}
	if binary.BigEndian.Uint32(prefix[:]) != frameSize {
		return frame{}, nil, fmt.Errorf("invalid frame length")
	}
	b := make([]byte, frameSize+4)
	copy(b, prefix[:])
	if _, err := io.ReadFull(r, b[4:]); err != nil {
		return frame{}, nil, err
	}
	if b[4] != 1 || crc32.ChecksumIEEE(b[4:len(b)-4]) != binary.BigEndian.Uint32(b[len(b)-4:]) {
		return frame{}, nil, fmt.Errorf("corrupt frame")
	}
	return frame{string(b[5:21]), b[21], binary.BigEndian.Uint64(b[22:30]), binary.BigEndian.Uint64(b[30:38]), int64(binary.BigEndian.Uint64(b[38:46]))}, b, nil
}

type queuedFrame struct {
	Data    []byte
	Arrived time.Time
}

var ageBuckets = []float64{.01, .05, .1, .5, 1, 2, 4, 8, 16}

type queueStats struct {
	Subscriber    string  `json:"subscriber"`
	Depth         int     `json:"depth"`
	Maximum       int     `json:"maximum"`
	OldestSeconds float64 `json:"oldest_seconds"`
	Unacked       int     `json:"unacked"`
	Drops         uint64  `json:"drops"`
	Sent          uint64  `json:"sent"`
}

type delivery struct {
	Ingress        uint64       `json:"ingress"`
	Egress         uint64       `json:"egress"`
	Consumed       uint64       `json:"consumed"`
	Missing        uint64       `json:"missing"`
	Duplicates     uint64       `json:"duplicates"`
	OutOfOrder     uint64       `json:"out_of_order"`
	Corrupt        uint64       `json:"corrupt"`
	Reconnects     uint64       `json:"reconnects"`
	LastReceiveAge float64      `json:"last_receive_age_seconds"`
	Pending        int          `json:"pending"`
	PendingAge     float64      `json:"pending_age_seconds"`
	AgeBuckets     []uint64     `json:"age_buckets"`
	AgeCount       uint64       `json:"age_count"`
	AgeSum         float64      `json:"age_sum_seconds"`
	ClockErrors    uint64       `json:"clock_errors"`
	Unsent         uint64       `json:"unsent_snapshots"`
	Queues         []queueStats `json:"queues"`
}

type resources struct {
	CPUSeconds      float64 `json:"cpu_seconds"`
	RSSBytes        uint64  `json:"rss_bytes"`
	FDs             int     `json:"file_descriptors"`
	MemoryTotal     uint64  `json:"memory_total_bytes"`
	MemoryAvailable uint64  `json:"memory_available_bytes"`
	NetworkReceive  uint64  `json:"network_receive_bytes"`
	NetworkTransmit uint64  `json:"network_transmit_bytes"`
}

type record struct {
	Schema     int        `json:"schema_version"`
	Run        string     `json:"run_id"`
	Kind       string     `json:"resource_kind"`
	Process    string     `json:"process_id,omitempty"`
	Host       string     `json:"host_id"`
	Generation string     `json:"generation"`
	Group      string     `json:"metric_group"`
	Sequence   uint64     `json:"sample_seq"`
	Observed   time.Time  `json:"observed_at"`
	Interval   int        `json:"interval_ms"`
	Delivery   *delivery  `json:"delivery,omitempty"`
	Resources  *resources `json:"resources,omitempty"`
}

type nodeConfig struct {
	ID             string `json:"id"`
	Role           string `json:"role"`
	Chain          string `json:"chain"`
	Base           int    `json:"base"`
	Slot           int    `json:"slot"`
	Generation     string `json:"generation"`
	WireGeneration uint64 `json:"wire_generation"`
}

type manifest struct {
	Run            string            `json:"run_id"`
	Started        time.Time         `json:"started_at"`
	Nodes          []nodeConfig      `json:"nodes"`
	HostGeneration string            `json:"host_generation"`
	Versions       map[string]string `json:"versions"`
}

// Only the terminal harness writes this file; never exposed through the console.
type faultControl struct {
	Pause        bool `json:"pause"`
	Quiet        bool `json:"quiet"`
	Rate         int  `json:"rate"`
	DispatchRate int  `json:"dispatch_rate"`
	ConsumeRate  int  `json:"consume_rate"`
	TelemetryOff bool `json:"telemetry_off"`
}
