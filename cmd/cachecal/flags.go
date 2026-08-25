package main

import "flag"

// newFlagSet wires cachecal's flags. Split out so main stays a composition
// root and readPairs/writeCurve stay testable.
func newFlagSet(pairsPath, outPath *string, floor *float64, embedKind, baseURL, embedModel *string) *flag.FlagSet {
	fs := flag.NewFlagSet("cachecal", flag.ContinueOnError)
	fs.StringVar(pairsPath, "pairs", "testdata/calibration/seed-pairs.csv", "labeled pairs CSV (text_a,text_b,label)")
	fs.StringVar(outPath, "out", "calibration.csv", "output curve CSV path")
	fs.Float64Var(floor, "floor", 0.99, "precision floor for selecting τ*")
	fs.StringVar(embedKind, "embedder", "local", "embedder: local or openai")
	fs.StringVar(baseURL, "base-url", "", "OpenAI-compatible base URL (with -embedder openai)")
	fs.StringVar(embedModel, "embed-model", "", "embedding model name (with -embedder openai); API key via CACHECAL_API_KEY")
	return fs
}
