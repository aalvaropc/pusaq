package main

import (
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aalvaropc/pusaq/internal/calibrate"
)

func writeCSV(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "pairs.csv")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestReadPairsValid(t *testing.T) {
	path := writeCSV(t, "text_a,text_b,label\nhello,hi there,equivalent\ncats,dogs,trap\n")
	pairs, err := readPairs(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(pairs) != 2 || pairs[0].Label != calibrate.LabelEquivalent || pairs[1].Label != calibrate.LabelTrap {
		t.Fatalf("pairs = %+v", pairs)
	}
}

func TestReadPairsRejectsMalformed(t *testing.T) {
	tests := []struct {
		name    string
		content string
	}{
		{"bad header", "a,b,c\nx,y,equivalent\n"},
		{"bad label", "text_a,text_b,label\nx,y,maybe\n"},
		{"empty text", "text_a,text_b,label\n,y,trap\n"},
		{"no rows", "text_a,text_b,label\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := readPairs(writeCSV(t, tt.content)); err == nil {
				t.Fatal("malformed CSV accepted")
			}
		})
	}
}

func TestWriteCurveMarksUndefinedValues(t *testing.T) {
	res := calibrate.Result{Curve: []calibrate.Point{
		{Tau: 0.9, Hits: 4, FalseHits: 1, Precision: 0.75, PrecisionLower: 0.3, Recall: 0.5, F1: 0.6},
		{Tau: 0.95, Precision: math.NaN(), PrecisionLower: math.NaN(), Recall: 0, F1: 0},
	}}
	path := filepath.Join(t.TempDir(), "curve.csv")
	if err := writeCurve(path, res); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	want := "tau,hits,false_hits,precision,precision_lower,recall,f1\n" +
		"0.900,4,1,0.750000,0.300000,0.500000,0.600000\n" +
		"0.950,0,0,undefined,undefined,0.000000,0.000000\n"
	if string(got) != want {
		t.Fatalf("curve =\n%s\nwant\n%s", got, want)
	}
}

func TestExplainFailureNamesTheMissingSupport(t *testing.T) {
	pairs := []calibrate.Pair{{Label: calibrate.LabelEquivalent}, {Label: calibrate.LabelTrap}}
	tests := []struct {
		name  string
		curve []calibrate.Point
		want  []string
	}{
		{"no τ reaches the floor", []calibrate.Point{{Tau: 0.9, Hits: 2, FalseHits: 1, Precision: 0.5, PrecisionLower: 0.1}},
			[]string{"no τ in the sweep reaches precision ≥ 0.990", "at least 299 predicted hits", "1 equivalent pairs"}},
		{"one hit", []calibrate.Point{{Tau: 0.855, Hits: 1, Precision: 1, PrecisionLower: 0.05}},
			[]string{"τ = 0.855 reaches precision 1.0000, but from 1 predicted hit(s) its 95% lower bound 0.0500", "at least 299 predicted hits"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := explainFailure(calibrate.Result{Curve: tt.curve}, 0.99, 0.95, pairs)
			for _, w := range tt.want {
				if !strings.Contains(err.Error(), w) {
					t.Fatalf("error %q lacks %q", err, w)
				}
			}
		})
	}
}

func TestValidateRule(t *testing.T) {
	tests := []struct {
		floor, confidence float64
		ok                bool
	}{
		{0.99, 0.95, true},
		{0.99, 0.5, true},
		{1, 0.95, false},
		{0, 0.95, false},
		{0.99, 1, false},
		{0.99, 0.49, false},
		{math.NaN(), 0.95, false},
	}
	for _, tt := range tests {
		if err := validateRule(tt.floor, tt.confidence); (err == nil) != tt.ok {
			t.Fatalf("validateRule(%g, %g) = %v, want ok=%t", tt.floor, tt.confidence, err, tt.ok)
		}
	}
}

func TestSeedDatasetParses(t *testing.T) {
	pairs, err := readPairs("../../testdata/calibration/seed-pairs.csv")
	if err != nil {
		t.Fatal(err)
	}
	var eq, tr int
	for _, p := range pairs {
		if p.Label == calibrate.LabelEquivalent {
			eq++
		} else {
			tr++
		}
	}
	if eq < 25 || tr < 25 {
		t.Fatalf("seed dataset has %d equivalent + %d trap pairs, want ≥25 each", eq, tr)
	}
}
