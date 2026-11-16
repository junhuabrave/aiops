// Resource-lab is a Linux-only, credential-free diagnostic experiment.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: resource-lab diagnose --pid PID --path PATH [--duration 4s] [--device NAME] | run --case all [--duration 4s]")
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	switch args[0] {
	case "diagnose":
		flags := flag.NewFlagSet("diagnose", flag.ContinueOnError)
		pid := flags.Int("pid", os.Getpid(), "process to observe; default this CLI")
		path := flags.String("path", ".", "filesystem to stat (never written)")
		duration := flags.Duration("duration", 4*time.Second, "observation window, 3s through 8s")
		device := flags.String("device", "", "optional /proc/diskstats block device name; association with path is NOT assumed")
		if err := flags.Parse(args[1:]); err != nil {
			return err
		}
		if *pid <= 0 || flags.NArg() != 0 {
			return fmt.Errorf("positive PID and no positional arguments required")
		}
		if err := validDuration(*duration); err != nil {
			return err
		}
		report, err := observe(ctx, *pid, *path, *device, *duration)
		if err != nil {
			return err
		}
		return writeJSON(report)
	case "run":
		flags := flag.NewFlagSet("run", flag.ContinueOnError)
		name := flags.String("case", "all", "all, healthy, cpu, memory, io, disk-full, ambiguous")
		duration := flags.Duration("duration", 4*time.Second, "observation window, 3s through 8s")
		if err := flags.Parse(args[1:]); err != nil {
			return err
		}
		if flags.NArg() != 0 {
			return fmt.Errorf("unexpected positional arguments")
		}
		if err := validDuration(*duration); err != nil {
			return err
		}
		results, err := runHarness(ctx, *name, *duration)
		if err != nil {
			return err
		}
		return writeJSON(results)
	case "_sandbox":
		if len(args) != 4 {
			return fmt.Errorf("internal sandbox arguments required")
		}
		d, err := time.ParseDuration(args[3])
		if err != nil {
			return err
		}
		return sandbox(ctx, args[1], args[2], d)
	case "_load":
		if len(args) != 5 {
			return fmt.Errorf("internal load arguments required")
		}
		d, err := time.ParseDuration(args[3])
		if err != nil {
			return err
		}
		parent, err := strconv.Atoi(args[4])
		if err != nil || parent != 1 || os.Getppid() != parent {
			return fmt.Errorf("loads require the isolated PID-1 coordinator")
		}
		return load(ctx, args[1], args[2], d)
	default:
		return fmt.Errorf("unknown command %q", args[0])
	}
}

func validDuration(d time.Duration) error {
	if d < 3*time.Second || d > 8*time.Second {
		return fmt.Errorf("duration must be between 3s and 8s")
	}
	return nil
}

func writeJSON(value any) error {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(value)
}
