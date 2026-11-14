// Command cachereplay replays labeled, Zipf-distributed traffic through the shadow cache's evaluation path and reports hit-rate with its false-positive rate, net savings, latency and the break-even hit-rate.
package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/aalvaropc/pusaq/internal/embed"
	"github.com/aalvaropc/pusaq/internal/replay"
)

func main() {
	if err := run(os.Args[1:], os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "cachereplay:", err)
		os.Exit(1)
	}
}

func run(args []string, stdout io.Writer) error {
	var o options
	if err := newFlagSet(&o).Parse(args); err != nil {
		return err
	}
	if err := o.validate(); err != nil {
		return err
	}
	corpus, err := readCorpus(o.corpus)
	if err != nil {
		return err
	}
	reqs, err := replay.Traffic(corpus, replay.TrafficOptions{Requests: o.requests, Exponent: o.zipf, Seed: o.seed, Latency: o.latency()})
	if err != nil {
		return err
	}
	start := time.Now()
	r, err := replay.Run(embed.Local{}, corpus, reqs, replay.Config{
		Prices:       replay.Prices{Input: o.inputPrice, Output: o.outputPrice, Embed: o.embedPrice},
		OutputTokens: o.outputTokens,
		IndexEntries: o.indexEntries,
		IndexBytes:   o.indexBytes,
	})
	if err != nil {
		return err
	}
	res := result{opts: o, replay: r, baseline: r.Baseline(), chosen: r.At(o.tau), sweep: r.Sweep(o.taus()), confusions: r.Confusions(o.tau), elapsed: time.Since(start)}

	if err := os.MkdirAll(o.out, 0o755); err != nil {
		return err
	}
	for name, write := range map[string]func(io.Writer) error{
		"sweep.csv":    res.writeSweep,
		"requests.csv": res.writeRequests,
		"summary.md":   res.writeSummary,
	} {
		if err := writeFile(filepath.Join(o.out, name), write); err != nil {
			return err
		}
	}
	return res.writeBrief(stdout)
}

func readCorpus(path string) (*replay.Corpus, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	c, err := replay.ReadCorpus(f)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return c, nil
}

func writeFile(path string, write func(io.Writer) error) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	if err := write(f); err != nil {
		_ = f.Close()
		return fmt.Errorf("%s: %w", path, err)
	}
	return f.Close()
}
