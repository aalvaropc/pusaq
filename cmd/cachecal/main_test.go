package main

import (
	"os"
	"path/filepath"
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
