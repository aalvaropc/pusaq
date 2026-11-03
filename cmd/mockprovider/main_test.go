package main

import (
	"testing"
	"time"
)

func TestLatencyMaxFlagAndItsP99Spelling(t *testing.T) {
	for _, args := range [][]string{
		{"--latency-p50", "200ms", "--latency-max", "800ms"},
		{"--latency-p50", "200ms", "--latency-p99", "800ms"},
	} {
		addr, opts, err := parseFlags(args)
		if err != nil {
			t.Fatalf("%v: %v", args, err)
		}
		if addr != ":9090" || opts.LatencyP50 != 200*time.Millisecond || opts.LatencyMax != 800*time.Millisecond {
			t.Fatalf("%v: addr %q, p50 %s, max %s", args, addr, opts.LatencyP50, opts.LatencyMax)
		}
		if opts.StreamChunkDelay != 10*time.Millisecond {
			t.Fatalf("%v: stream chunk delay %s, want the 10ms default", args, opts.StreamChunkDelay)
		}
	}
}
