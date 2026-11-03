package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const fixture = "../../internal/report/testdata/events.log"

func TestRunWritesReportAndSummary(t *testing.T) {
	out := filepath.Join(t.TempDir(), "report.html")
	var stdout bytes.Buffer
	if err := run([]string{"-in", fixture, "-out", out, "-run-id", "r1"}, strings.NewReader(""), &stdout); err != nil {
		t.Fatal(err)
	}
	html, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(html), "<!doctype html>") || !strings.Contains(string(html), "run_id <code>r1</code>") {
		t.Fatalf("report does not start as HTML for run r1: %.120s", html)
	}
	for _, want := range []string{"events: 10 of 11 cost events (run_id r1) from " + fixture, "report: " + out} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("summary lacks %q:\n%s", want, stdout.String())
		}
	}
}

func TestRunReadsStandardInput(t *testing.T) {
	log, err := os.ReadFile(fixture)
	if err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(t.TempDir(), "report.html")
	var stdout bytes.Buffer
	if err := run([]string{"-out", out}, bytes.NewReader(log), &stdout); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout.String(), "events: 11 of 11 cost events (all runs) from standard input") {
		t.Fatalf("summary:\n%s", stdout.String())
	}
}

func TestRunFailsWithoutEventsAndWritesNothing(t *testing.T) {
	out := filepath.Join(t.TempDir(), "report.html")
	for _, args := range [][]string{
		{"-out", out},
		{"-in", fixture, "-out", out, "-run-id", "absent"},
	} {
		var stdout bytes.Buffer
		err := run(args, strings.NewReader("plain text\n{\"msg\":\"gateway listening\"}\n"), &stdout)
		if err == nil || !strings.Contains(err.Error(), "no cost_event records") {
			t.Fatalf("run %v err = %v", args, err)
		}
		if _, statErr := os.Stat(out); !os.IsNotExist(statErr) || stdout.Len() != 0 {
			t.Fatalf("run %v left output: stat %v stdout %q", args, statErr, stdout.String())
		}
	}
}

func TestRunRejectsBadArguments(t *testing.T) {
	for _, args := range [][]string{{"extra"}, {"-unknown"}, {"-in", filepath.Join(t.TempDir(), "missing.log")}} {
		if err := run(args, strings.NewReader(""), &bytes.Buffer{}); err == nil {
			t.Fatalf("run %v succeeded", args)
		}
	}
}
