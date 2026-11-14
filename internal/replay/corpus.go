// Package replay replays labeled traffic through the shadow cache's evaluation path and measures hit-rate, false hits, savings and latency.
package replay

import (
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"slices"
)

// Prompt is one labeled paraphrase of the corpus.
type Prompt struct {
	Intent string
	Lang   string
	Family string
	Kind   string
	Text   string
}

// Answer labels the reference answer a prompt needs: its intent in its language.
func (p Prompt) Answer() string { return p.Intent + "/" + p.Lang }

// Corpus is a validated set of labeled prompts grouped by answer.
type Corpus struct {
	Prompts []Prompt
	Answers [][]int
}

var kinds = []string{"none", "entity", "action", "negation", "date", "quantity"}

// NewCorpus validates prompts and groups them by answer in order of first appearance.
func NewCorpus(prompts []Prompt) (*Corpus, error) {
	if len(prompts) == 0 {
		return nil, errors.New("corpus has no prompts")
	}
	c := &Corpus{Prompts: slices.Clone(prompts)}
	group := map[string]int{}
	texts := map[string]int{}
	intentFamily := map[string]string{}
	familyKind := map[string]string{}
	familyIntents := map[string]map[string]bool{}
	for i, p := range c.Prompts {
		n := i + 1
		if p.Intent == "" || p.Lang == "" || p.Family == "" || p.Text == "" {
			return nil, fmt.Errorf("prompt %d: intent, lang, family and text are required", n)
		}
		if !slices.Contains(kinds, p.Kind) {
			return nil, fmt.Errorf("prompt %d: kind %q, want one of %v", n, p.Kind, kinds)
		}
		if j, dup := texts[p.Text]; dup {
			return nil, fmt.Errorf("prompt %d repeats the text of prompt %d", n, j+1)
		}
		texts[p.Text] = i
		if f, ok := intentFamily[p.Intent]; ok && f != p.Family {
			return nil, fmt.Errorf("prompt %d: intent %q is in families %q and %q", n, p.Intent, f, p.Family)
		}
		intentFamily[p.Intent] = p.Family
		if k, ok := familyKind[p.Family]; ok && k != p.Kind {
			return nil, fmt.Errorf("prompt %d: family %q has kinds %q and %q", n, p.Family, k, p.Kind)
		}
		familyKind[p.Family] = p.Kind
		if familyIntents[p.Family] == nil {
			familyIntents[p.Family] = map[string]bool{}
		}
		familyIntents[p.Family][p.Intent] = true
		g, ok := group[p.Answer()]
		if !ok {
			g = len(c.Answers)
			group[p.Answer()] = g
			c.Answers = append(c.Answers, nil)
		}
		c.Answers[g] = append(c.Answers[g], i)
	}
	families := make([]string, 0, len(familyIntents))
	for f := range familyIntents {
		families = append(families, f)
	}
	slices.Sort(families)
	for _, f := range families {
		count := len(familyIntents[f])
		switch {
		case familyKind[f] == "none" && count != 1:
			return nil, fmt.Errorf("family %q of kind none holds %d intents, want 1", f, count)
		case familyKind[f] != "none" && count < 2:
			return nil, fmt.Errorf("family %q of kind %s holds one intent, want look-alike intents", f, familyKind[f])
		}
	}
	return c, nil
}

// ReadCorpus parses a CSV corpus with the header intent,lang,family,kind,text.
func ReadCorpus(r io.Reader) (*Corpus, error) {
	rows, err := csv.NewReader(r).ReadAll()
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 || !slices.Equal(rows[0], []string{"intent", "lang", "family", "kind", "text"}) {
		return nil, errors.New("header must be intent,lang,family,kind,text")
	}
	prompts := make([]Prompt, 0, len(rows)-1)
	for _, row := range rows[1:] {
		prompts = append(prompts, Prompt{Intent: row[0], Lang: row[1], Family: row[2], Kind: row[3], Text: row[4]})
	}
	return NewCorpus(prompts)
}

// Families counts the corpus' families.
func (c *Corpus) Families() int {
	seen := map[string]bool{}
	for _, p := range c.Prompts {
		seen[p.Family] = true
	}
	return len(seen)
}
