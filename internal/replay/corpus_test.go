package replay

import (
	"os"
	"strings"
	"testing"
)

const corpusPath = "../../testdata/replay/intents.csv"

func loadCorpus(t *testing.T) *Corpus {
	t.Helper()
	f, err := os.Open(corpusPath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	c, err := ReadCorpus(f)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestShippedCorpusShape(t *testing.T) {
	c := loadCorpus(t)
	intents := map[string]map[string]int{}
	families := map[string]map[string]bool{}
	for _, p := range c.Prompts {
		if intents[p.Intent] == nil {
			intents[p.Intent] = map[string]int{}
		}
		intents[p.Intent][p.Lang]++
		if families[p.Kind] == nil {
			families[p.Kind] = map[string]bool{}
		}
		families[p.Kind][p.Family] = true
	}
	if len(intents) < 40 {
		t.Fatalf("intents = %d, want dozens", len(intents))
	}
	for intent, langs := range intents {
		if len(langs) != 2 || langs["en"] < 4 || langs["es"] < 4 {
			t.Errorf("intent %s paraphrases by language = %v, want at least 4 in en and es", intent, langs)
		}
	}
	for _, kind := range []string{"entity", "action", "negation", "date", "quantity"} {
		if len(families[kind]) < 4 {
			t.Errorf("trap kind %s has %d families, want at least 4", kind, len(families[kind]))
		}
	}
	t.Logf("%d prompts, %d answers, %d intents, %d families", len(c.Prompts), len(c.Answers), len(intents), c.Families())
}

func TestShippedCorpusEligibility(t *testing.T) {
	c := loadCorpus(t)
	for _, p := range c.Prompts {
		skip := skipReason(p.Text)
		if p.Intent == "exchange-rate" {
			if skip != "temporal" {
				t.Errorf("time-dependent prompt %q skip = %q, want temporal", p.Text, skip)
			}
			continue
		}
		if skip != "" {
			t.Errorf("prompt %q skip = %q, want eligible", p.Text, skip)
		}
	}
}

func TestReadCorpusRejectsInvalidInput(t *testing.T) {
	const header = "intent,lang,family,kind,text\n"
	tests := []struct {
		name, csv, want string
	}{
		{"header", "intent,lang,text\na,en,x\n", "header"},
		{"empty field", header + "a,en,f,entity,\n", "required"},
		{"unknown kind", header + "a,en,f,synonym,x\n", "kind"},
		{"duplicate text", header + "a,en,f,entity,x\nb,en,f,entity,x\n", "repeats"},
		{"intent in two families", header + "a,en,f,entity,x\na,es,g,entity,y\nb,en,f,entity,z\n", "families"},
		{"family with two kinds", header + "a,en,f,entity,x\nb,en,f,date,y\n", "kinds"},
		{"none family with look-alikes", header + "a,en,f,none,x\nb,en,f,none,y\n", "want 1"},
		{"trap family alone", header + "a,en,f,entity,x\n", "look-alike"},
		{"no prompts", header, "no prompts"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := ReadCorpus(strings.NewReader(tt.csv))
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("err = %v, want it to mention %q", err, tt.want)
			}
		})
	}
}

func TestCorpusGroupsByIntentAndLanguage(t *testing.T) {
	c, err := ReadCorpus(strings.NewReader("intent,lang,family,kind,text\na,en,f,entity,x\nb,en,f,entity,y\na,es,f,entity,z\na,en,f,entity,w\n"))
	if err != nil {
		t.Fatal(err)
	}
	want := [][]int{{0, 3}, {1}, {2}}
	if len(c.Answers) != len(want) {
		t.Fatalf("answers = %v, want %v", c.Answers, want)
	}
	for i := range want {
		if len(c.Answers[i]) != len(want[i]) || c.Answers[i][0] != want[i][0] {
			t.Fatalf("answers = %v, want %v", c.Answers, want)
		}
	}
}
