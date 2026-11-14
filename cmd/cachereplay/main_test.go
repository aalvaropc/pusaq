package main

import (
	"bytes"
	"encoding/csv"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

const corpus = "../../testdata/replay/intents.csv"

func defaults(t *testing.T) options {
	t.Helper()
	var o options
	if err := newFlagSet(&o).Parse(nil); err != nil {
		t.Fatal(err)
	}
	return o
}

func TestDefaultFlagsAreValid(t *testing.T) {
	o := defaults(t)
	if err := o.validate(); err != nil {
		t.Fatal(err)
	}
	if o.tau != 0.95 || o.requests != 5000 || o.zipf != 1 || o.indexEntries != 10000 || o.indexBytes != 64<<20 {
		t.Fatalf("defaults = %+v", o)
	}
}

func TestFlagValidationRejectsEachInvalidValue(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*options)
		want   string
	}{
		{"no requests", func(o *options) { o.requests = 0 }, "-requests"},
		{"too many requests", func(o *options) { o.requests = maxRequests + 1 }, "-requests"},
		{"negative zipf", func(o *options) { o.zipf = -0.5 }, "-zipf"},
		{"NaN zipf", func(o *options) { o.zipf = math.NaN() }, "-zipf"},
		{"infinite zipf", func(o *options) { o.zipf = math.Inf(1) }, "-zipf"},
		{"zero tau", func(o *options) { o.tau = 0 }, "-tau"},
		{"tau above one", func(o *options) { o.tau = 1.01 }, "-tau"},
		{"NaN tau", func(o *options) { o.tau = math.NaN() }, "-tau"},
		{"inverted sweep", func(o *options) { o.sweepFrom, o.sweepTo = 0.9, 0.8 }, "-sweep-from"},
		{"sweep above one", func(o *options) { o.sweepTo = 1.5 }, "-sweep-from"},
		{"zero step", func(o *options) { o.sweepStep = 0 }, "-sweep-step"},
		{"too many points", func(o *options) { o.sweepStep = 1e-6 }, "-sweep-step"},
		{"negative price", func(o *options) { o.embedPrice = -1 }, "-embed-price"},
		{"infinite price", func(o *options) { o.inputPrice = math.Inf(1) }, "-input-price"},
		{"negative output tokens", func(o *options) { o.outputTokens = -1 }, "-output-tokens"},
		{"no index entries", func(o *options) { o.indexEntries = 0 }, "-index-entries"},
		{"no index bytes", func(o *options) { o.indexBytes = 0 }, "-index-bytes"},
		{"max below p50", func(o *options) { o.latencyMax = time.Millisecond }, "latency-max"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			o := defaults(t)
			tt.mutate(&o)
			err := o.validate()
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("err = %v, want it to name %s", err, tt.want)
			}
		})
	}
}

func TestSweepIncludesBoundsAndTheChosenTau(t *testing.T) {
	o := defaults(t)
	taus := o.taus()
	if len(taus) != 40 || taus[0] != 0.8 || taus[39] != 0.995 || !slices.Contains(taus, 0.95) {
		t.Fatalf("default sweep = %v, want 40 points from 0.8 to 0.995 with 0.95", taus)
	}
	o.tau = 0.9512
	if taus := o.taus(); len(taus) != 41 || !slices.Contains(taus, 0.9512) || !slices.IsSorted(taus) {
		t.Fatalf("sweep with an off-grid τ = %v", taus)
	}
}

func readCSV(t *testing.T, path string) [][]string {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	rows, err := csv.NewReader(f).ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	return rows
}

func TestRunWritesSweepRequestsAndSummary(t *testing.T) {
	dir := t.TempDir()
	var stdout bytes.Buffer
	if err := run([]string{"-corpus", corpus, "-out", dir, "-requests", "300", "-seed", "5"}, &stdout); err != nil {
		t.Fatal(err)
	}
	sweep := readCSV(t, filepath.Join(dir, "sweep.csv"))
	header := sweep[0]
	hit, fp := slices.Index(header, "hit_rate"), slices.Index(header, "fp_rate")
	if hit < 0 || fp != hit+1 {
		t.Fatalf("sweep header = %v, want fp_rate right after hit_rate", header)
	}
	if len(sweep) != 41 {
		t.Fatalf("sweep rows = %d, want 40 τ plus the header", len(sweep))
	}
	chosen := 0
	for _, row := range sweep[1:] {
		if row[1] == "true" {
			chosen++
			if row[0] != "0.95" {
				t.Fatalf("chosen row τ = %s, want 0.95", row[0])
			}
		}
	}
	if chosen != 1 {
		t.Fatalf("chosen rows = %d, want 1", chosen)
	}
	if requests := readCSV(t, filepath.Join(dir, "requests.csv")); len(requests) != 301 {
		t.Fatalf("request rows = %d, want 300 plus the header", len(requests))
	}
	summary, err := os.ReadFile(filepath.Join(dir, "summary.md"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"| Hit-rate | False hits | False-positive rate |", "Break-even correct-hit rate", "Without cache (provider)", "Kept out: `temporal`", "not production traffic"} {
		if !strings.Contains(string(summary), want) {
			t.Errorf("summary lacks %q", want)
		}
	}
	if !strings.Contains(stdout.String(), "with false-positive rate") {
		t.Fatalf("stdout = %q, want the hit-rate with its false-positive rate", stdout.String())
	}
}

func TestRunIsDeterministicExceptMeasuredTimes(t *testing.T) {
	rows := func() [][]string {
		dir := t.TempDir()
		if err := run([]string{"-corpus", corpus, "-out", dir, "-requests", "200"}, &bytes.Buffer{}); err != nil {
			t.Fatal(err)
		}
		out := readCSV(t, filepath.Join(dir, "requests.csv"))
		lookup := slices.Index(out[0], "lookup_ms")
		for _, row := range out {
			row[lookup] = ""
		}
		return out
	}
	a, b := rows(), rows()
	if !slices.EqualFunc(a, b, slices.Equal[[]string]) {
		t.Fatal("two runs with the same flags produced different requests")
	}
}

func TestRunRejectsInvalidInput(t *testing.T) {
	dir := t.TempDir()
	bad := filepath.Join(dir, "bad.csv")
	if err := os.WriteFile(bad, []byte("intent,lang,text\na,en,x\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"-corpus", bad, "-out", dir},
		{"-corpus", filepath.Join(dir, "missing.csv"), "-out", dir},
		{"-corpus", corpus, "-out", dir, "-tau", "2"},
		{"-unknown"},
	} {
		if err := run(args, &bytes.Buffer{}); err == nil {
			t.Errorf("run(%v) succeeded", args)
		}
	}
}
