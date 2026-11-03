// Command cachecal calibrates the semantic cache's similarity threshold τ:
// it scores a labeled pair dataset with the same embedder implementations
// the gateway uses, sweeps τ, writes the precision/recall curve, and prints
// τ* — or fails loudly when no τ qualifies.
// A τ calibrated with a different embedder than production runs with is
// meaningless; sharing internal/embed makes that mistake hard.
package main

import (
	"context"
	"encoding/csv"
	"fmt"
	"math"
	"os"
	"strconv"
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
		confidence float64
		embedKind  string
		baseURL    string
		embedModel string
	)
	fs := newFlagSet(&pairsPath, &outPath, &floor, &confidence, &embedKind, &baseURL, &embedModel)
	if err := fs.Parse(os.Args[1:]); err != nil {
		return err
	}
	if err := validateRule(floor, confidence); err != nil {
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
	res := calibrate.Sweep(scored, floor, confidence)
	res.Embedder = embedder.Name()

	if err := writeCurve(outPath, res); err != nil {
		return err
	}

	fmt.Printf("dataset: %d pairs (%s)\n", len(pairs), pairsPath)
	fmt.Printf("embedder: %s\n", res.Embedder)
	fmt.Printf("curve: %s\n", outPath)
	if !res.Qualified {
		return explainFailure(res, floor, confidence, pairs)
	}
	fmt.Printf("τ* = %.3f  (precision %.4f, lower bound %.4f at %s confidence, %d predicted hits, recall %.4f, floor %.3f)\n",
		res.TauStar.Tau, res.TauStar.Precision, res.TauStar.PrecisionLower, percent(confidence), res.TauStar.Hits, res.TauStar.Recall, floor)
	fmt.Println("note: the bound holds per τ and for this dataset's pairs, not for production traffic; a τ from a seed dataset proves the harness, not a production threshold; and numbers are only meaningful for the embedder that produced them.")
	return nil
}

func validateRule(floor, confidence float64) error {
	if !(floor > 0 && floor < 1) {
		return fmt.Errorf("-floor must be in (0, 1), got %g", floor)
	}
	if !(confidence >= 0.5 && confidence < 1) {
		return fmt.Errorf("-confidence must be in [0.5, 1), got %g", confidence)
	}
	return nil
}

func percent(confidence float64) string {
	return strconv.FormatFloat(confidence*100, 'f', -1, 64) + "%"
}

func explainFailure(res calibrate.Result, floor, confidence float64, pairs []calibrate.Pair) error {
	equivalents := 0
	for _, p := range pairs {
		if p.Label == calibrate.LabelEquivalent {
			equivalents++
		}
	}
	reason := fmt.Sprintf("no τ in the sweep reaches precision ≥ %.3f with recall > 0 — this embedder cannot separate the traps in this dataset", floor)
	for _, p := range res.Curve {
		if p.Precision >= floor {
			reason = fmt.Sprintf("τ = %.3f reaches precision %.4f, but from %d predicted hit(s) its %s lower bound %.4f is below the floor %.3f",
				p.Tau, p.Precision, p.Hits, percent(confidence), p.PrecisionLower, floor)
			break
		}
	}
	return fmt.Errorf("no τ certified: %s; certifying precision ≥ %.3f at %s confidence needs at least %d predicted hits with no false hit (this dataset has %d equivalent pairs); do not deploy a τ from it",
		reason, floor, percent(confidence), calibrate.MinHits(floor, confidence), equivalents)
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
	if err := w.Write([]string{"tau", "hits", "false_hits", "precision", "precision_lower", "recall", "f1"}); err != nil {
		return err
	}
	for _, p := range res.Curve {
		if err := w.Write([]string{
			fmt.Sprintf("%.3f", p.Tau),
			strconv.Itoa(p.Hits),
			strconv.Itoa(p.FalseHits),
			cell(p.Precision),
			cell(p.PrecisionLower),
			cell(p.Recall),
			cell(p.F1),
		}); err != nil {
			return err
		}
	}
	w.Flush()
	return w.Error()
}

func cell(v float64) string {
	if math.IsNaN(v) {
		return "undefined"
	}
	return fmt.Sprintf("%.6f", v)
}
