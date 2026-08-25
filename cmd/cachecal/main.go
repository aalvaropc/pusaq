// Command cachecal calibrates the semantic cache's similarity threshold τ:
// it scores a labeled pair dataset with the same embedder implementations
// the gateway uses, sweeps τ, writes the precision/recall curve, and prints
// τ* = min{τ : precision ≥ floor} — or fails loudly when no τ qualifies.
// A τ calibrated with a different embedder than production runs with is
// meaningless; sharing internal/embed makes that mistake hard.
package main

import (
	"context"
	"encoding/csv"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/aalvaropc/pusaq/internal/calibrate"
	"github.com/aalvaropc/pusaq/internal/embed"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "cachecal:", err)
		os.Exit(1)
	}
}

func run() error {
	var (
		pairsPath  string
		outPath    string
		floor      float64
		embedKind  string
		baseURL    string
		embedModel string
	)
	fs := newFlagSet(&pairsPath, &outPath, &floor, &embedKind, &baseURL, &embedModel)
	if err := fs.Parse(os.Args[1:]); err != nil {
		return err
	}

	embedder, err := buildEmbedder(embedKind, baseURL, embedModel)
	if err != nil {
		return err
	}

	pairs, err := readPairs(pairsPath)
	if err != nil {
		return err
	}

	scored, err := calibrate.Score(context.Background(), embedder, pairs)
	if err != nil {
		return fmt.Errorf("scoring pairs: %w", err)
	}
	res := calibrate.Sweep(scored, floor)
	res.Embedder = embedder.Name()

	if err := writeCurve(outPath, res); err != nil {
		return err
	}

	fmt.Printf("dataset: %d pairs (%s)\n", len(pairs), pairsPath)
	fmt.Printf("embedder: %s\n", res.Embedder)
	fmt.Printf("curve: %s\n", outPath)
	if !res.Qualified {
		return fmt.Errorf("no τ in the sweep reaches precision ≥ %.3f with recall > 0 — this embedder cannot separate the traps in this dataset; do not deploy a τ from it", floor)
	}
	fmt.Printf("τ* = %.3f  (precision %.4f, recall %.4f, floor %.3f)\n",
		res.TauStar.Tau, res.TauStar.Precision, res.TauStar.Recall, floor)
	fmt.Println("note: a τ from a seed dataset proves the harness, not a production threshold; and numbers are only meaningful for the embedder that produced them.")
	return nil
}

func buildEmbedder(kind, baseURL, model string) (embed.Embedder, error) {
	switch kind {
	case "local":
		return embed.Local{}, nil
	case "openai":
		if baseURL == "" || model == "" {
			return nil, fmt.Errorf("-base-url and -embed-model are required with -embedder openai")
		}
		return embed.NewHTTP("openai", strings.TrimRight(baseURL, "/"), model,
			os.Getenv("CACHECAL_API_KEY"), 30*time.Second), nil
	default:
		return nil, fmt.Errorf("unknown embedder %q, want local or openai", kind)
	}
}

// readPairs parses the labeled CSV: header text_a,text_b,label with label
// equivalent|trap. Malformed rows are errors, not skips — a silently
// shrunken dataset would silently change τ*.
func readPairs(path string) ([]calibrate.Pair, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()

	r := csv.NewReader(f)
	rows, err := r.ReadAll()
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", path, err)
	}
	if len(rows) < 2 {
		return nil, fmt.Errorf("%s: no data rows", path)
	}
	if h := rows[0]; len(h) != 3 || h[0] != "text_a" || h[1] != "text_b" || h[2] != "label" {
		return nil, fmt.Errorf("%s: header must be text_a,text_b,label", path)
	}

	pairs := make([]calibrate.Pair, 0, len(rows)-1)
	for i, row := range rows[1:] {
		label := calibrate.Label(row[2])
		if label != calibrate.LabelEquivalent && label != calibrate.LabelTrap {
			return nil, fmt.Errorf("%s row %d: label %q, want equivalent or trap", path, i+2, row[2])
		}
		if row[0] == "" || row[1] == "" {
			return nil, fmt.Errorf("%s row %d: empty text", path, i+2)
		}
		pairs = append(pairs, calibrate.Pair{A: row[0], B: row[1], Label: label})
	}
	return pairs, nil
}

func writeCurve(path string, res calibrate.Result) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()

	w := csv.NewWriter(f)
	if err := w.Write([]string{"tau", "precision", "recall", "f1"}); err != nil {
		return err
	}
	for _, p := range res.Curve {
		if err := w.Write([]string{
			fmt.Sprintf("%.3f", p.Tau),
			fmt.Sprintf("%.6f", p.Precision),
			fmt.Sprintf("%.6f", p.Recall),
			fmt.Sprintf("%.6f", p.F1),
		}); err != nil {
			return err
		}
	}
	w.Flush()
	return w.Error()
}
