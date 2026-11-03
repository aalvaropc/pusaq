// Command eventreport turns the gateway's JSON log lines into a self-contained HTML report.
package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/aalvaropc/pusaq/internal/report"
)

func main() {
	if err := run(os.Args[1:], os.Stdin, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "eventreport:", err)
		os.Exit(1)
	}
}

func run(args []string, stdin io.Reader, stdout io.Writer) error {
	fs := flag.NewFlagSet("eventreport", flag.ContinueOnError)
	inPath := fs.String("in", "-", "gateway log file to read, or - for standard input")
	outPath := fs.String("out", "dashboard.html", "HTML report to write")
	runID := fs.String("run-id", "", "keep only cost events with this run_id")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if fs.NArg() > 0 {
		return fmt.Errorf("unexpected arguments %q", fs.Args())
	}

	in, name, err := open(*inPath, stdin)
	if err != nil {
		return err
	}
	parsed, err := report.Parse(in)
	_ = in.Close()
	if err != nil {
		return fmt.Errorf("reading %s: %w", name, err)
	}

	r, err := report.Build(parsed, report.Options{RunID: *runID})
	if err != nil {
		return fmt.Errorf("%s: %w", name, err)
	}
	if err := writeHTML(*outPath, r, name); err != nil {
		return err
	}
	if err := report.WriteSummary(stdout, r, name); err != nil {
		return err
	}
	_, err = fmt.Fprintf(stdout, "report: %s\n", *outPath)
	return err
}

func open(path string, stdin io.Reader) (io.ReadCloser, string, error) {
	if path == "-" {
		return io.NopCloser(stdin), "standard input", nil
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, "", err
	}
	return f, path, nil
}

func writeHTML(path string, r *report.Report, input string) (err error) {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, f.Close()) }()
	return report.WriteHTML(f, r, input)
}
