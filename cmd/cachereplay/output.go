package main

import (
	"bufio"
	"encoding/csv"
	"fmt"
	"io"
	"math"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/aalvaropc/pusaq/internal/replay"
)

const topConfusions = 15

type result struct {
	opts       options
	replay     *replay.Replay
	baseline   replay.Baseline
	chosen     replay.Point
	sweep      []replay.Point
	confusions []replay.Confusion
	elapsed    time.Duration
}

func (res result) writeSweep(w io.Writer) error {
	cw := csv.NewWriter(w)
	_ = cw.Write([]string{"tau", "chosen", "eligible", "hits", "false_hits", "hit_rate", "fp_rate", "correct_hits", "correct_hit_rate", "novel_hits", "novel_correct_hits", "avoided_usd", "wrong_hit_usd", "toll_usd", "net_usd", "p50_ms", "p95_ms"})
	for _, p := range res.sweep {
		_ = cw.Write([]string{
			tauText(p.Tau), strconv.FormatBool(p.Tau == res.opts.tau),
			strconv.Itoa(p.Eligible), strconv.Itoa(p.Hits), strconv.Itoa(p.FalseHits),
			rate(p.HitRate()), rate(p.FPRate()),
			strconv.Itoa(p.CorrectHits()), rate(p.CorrectHitRate()), strconv.Itoa(p.NovelHits), strconv.Itoa(p.NovelCorrectHits()),
			dollars(p.AvoidedUSD), dollars(p.WrongHitUSD), dollars(p.TollUSD), dollars(p.NetUSD()),
			millis(p.P50), millis(p.P95),
		})
	}
	cw.Flush()
	return cw.Error()
}

func (res result) writeRequests(w io.Writer) error {
	c := res.replay.Corpus
	cw := csv.NewWriter(w)
	_ = cw.Write([]string{"request", "intent", "lang", "family", "kind", "text", "skip", "neighbor_request", "neighbor_intent", "neighbor_lang", "similarity", "correct", "repeat", "hit_at_tau", "lookup_ms", "provider_ms", "provider_usd", "embed_usd"})
	for _, o := range res.replay.Outcomes {
		p := c.Prompts[o.Prompt]
		neighbor, intent, lang, sim := "", "", "", ""
		if o.Neighbor >= 0 {
			n := res.replay.Outcomes[o.Neighbor]
			np := c.Prompts[n.Prompt]
			neighbor, intent, lang, sim = n.ID, np.Intent, np.Lang, strconv.FormatFloat(o.Similarity, 'f', 6, 64)
		}
		_ = cw.Write([]string{
			o.ID, p.Intent, p.Lang, p.Family, p.Kind, p.Text, o.Skip,
			neighbor, intent, lang, sim,
			strconv.FormatBool(o.Neighbor >= 0 && o.Correct), strconv.FormatBool(o.Repeat), strconv.FormatBool(o.Hit(res.opts.tau)),
			millis(o.Lookup), millis(o.Provider), dollars(o.ProviderUSD), dollars(o.EmbedUSD),
		})
	}
	cw.Flush()
	return cw.Error()
}

func (res result) writeSummary(w io.Writer) error {
	o, b, p, c := res.opts, res.baseline, res.chosen, res.replay.Corpus
	bw := bufio.NewWriter(w)
	line := func(format string, args ...any) { _, _ = fmt.Fprintf(bw, format+"\n", args...) }

	line("# Semantic cache replay")
	line("")
	line("Every request went through the gateway's shadow evaluation path (namespace, content-rule eligibility, `%s` embedder, nearest neighbor, insertion after evaluation). Hit-rate is predicted hits over eligible requests; the false-positive rate is predicted hits whose neighbor carries a different labeled answer, over predicted hits. The corpus is synthetic, the `local` embedder is lexical, provider latencies come from the mock provider's model and prices from flags: these numbers describe this replay, not production traffic.", res.replay.Embedder)
	line("")
	line("## Parameters")
	line("")
	line("| Parameter | Value |")
	line("|---|---|")
	line("| Corpus | `%s`: %d prompts, %d answers (intent × language), %d families |", o.corpus, len(c.Prompts), len(c.Answers), c.Families())
	line("| Traffic | %d requests, Zipf exponent %g over answers, uniform paraphrase, seed %d |", o.requests, o.zipf, o.seed)
	line("| Embedder | `%s` |", res.replay.Embedder)
	line("| τ | %s; sweep %s–%s step %g |", tauText(o.tau), tauText(o.sweepFrom), tauText(o.sweepTo), o.sweepStep)
	line("| Prices (USD per 1M tokens) | input %g, output %g, embedding %g |", o.inputPrice, o.outputPrice, o.embedPrice)
	line("| Output tokens per answer | %d |", o.outputTokens)
	line("| Provider latency | mock provider model, p50 %s, max %s |", o.latencyP50, o.latencyMax)
	line("| Index caps | %d entries, %d bytes |", o.indexEntries, o.indexBytes)
	line("| Replay wall time | %s |", res.elapsed.Round(time.Millisecond))
	line("")
	line("## Result at τ = %s", tauText(o.tau))
	line("")
	line("| Eligible | Predicted hits | Hit-rate | False hits | False-positive rate | Correct hits | Correct-hit rate |")
	line("|---|---|---|---|---|---|---|")
	line("| %d of %d | %d | %s | %d | %s | %d | %s |", p.Eligible, p.Requests, p.Hits, percent(p.HitRate()), p.FalseHits, percent(p.FPRate()), p.CorrectHits(), percent(p.CorrectHitRate()))
	line("")
	line("| Hits on exact repeats | Hits on text not seen before | Correct among them | Exact-match cache on the same traffic |")
	line("|---|---|---|---|")
	line("| %d | %d | %d | %d hits, hit-rate %s, no false hits |", p.Hits-p.NovelHits, p.NovelHits, p.NovelCorrectHits(), b.Repeats, percent(ratio(b.Repeats, b.Eligible)))
	line("")
	line("An exact repeat finds its own earlier text while that entry is in the index, so its hit is correct; what the semantic cache adds over an exact-match cache is the correct hits on text not seen before.")
	line("")
	line("## Money (USD)")
	line("")
	line("| Provider spend without cache | Embedding toll | Avoided by correct hits | Net savings | Skipped by false hits (not savings) | Break-even correct-hit rate |")
	line("|---|---|---|---|---|---|")
	line("| %s | %s | %s | %s | %s | %s |", dollars6(b.ProviderUSD), dollars6(p.TollUSD), dollars6(p.AvoidedUSD), dollars6(p.NetUSD()), dollars6(p.WrongHitUSD), percent(b.BreakEven()))
	line("")
	line("Net savings = provider cost avoided by correct hits − embedding toll on every eligible request. The break-even rate assumes hits cost what the average eligible request costs; the measured correct-hit rate is %s.", percent(p.CorrectHitRate()))
	line("")
	line("## Latency")
	line("")
	line("| Arm | p50 | p95 |")
	line("|---|---|---|")
	line("| Without cache (provider) | %s | %s |", millisUnit(b.P50), millisUnit(b.P95))
	line("| With cache at τ = %s | %s | %s |", tauText(o.tau), millisUnit(p.P50), millisUnit(p.P95))
	line("| Embed + search, eligible requests | %s | %s |", millisUnit(b.LookupP50), millisUnit(b.LookupP95))
	line("")
	line("## Eligibility")
	line("")
	line("| Requests | Count |")
	line("|---|---|")
	line("| Eligible | %d |", b.Eligible)
	reasons := make([]string, 0, len(b.Skips))
	for reason := range b.Skips {
		reasons = append(reasons, reason)
	}
	slices.Sort(reasons)
	for _, reason := range reasons {
		line("| Kept out: `%s` | %d |", reason, b.Skips[reason])
	}
	line("| Eligible but not inserted | %d |", b.NotInserted)
	line("")
	line("## False hits at τ = %s", tauText(o.tau))
	line("")
	if len(res.confusions) == 0 {
		line("None.")
	} else {
		totals := map[string]int{}
		for _, cf := range res.confusions {
			key := cf.Category
			if cf.Kind != "" {
				key += " (" + cf.Kind + ")"
			}
			totals[key] += cf.Count
		}
		keys := make([]string, 0, len(totals))
		for k := range totals {
			keys = append(keys, k)
		}
		slices.Sort(keys)
		parts := make([]string, 0, len(keys))
		for _, k := range keys {
			parts = append(parts, fmt.Sprintf("%s %d", k, totals[k]))
		}
		line("By category: %s.", strings.Join(parts, ", "))
		line("")
		line("| Request answer | Neighbor answer | Category | Kind | False hits |")
		line("|---|---|---|---|---|")
		for i, cf := range res.confusions {
			if i == topConfusions {
				break
			}
			line("| %s | %s | %s | %s | %d |", cf.Request, cf.Neighbor, cf.Category, cf.Kind, cf.Count)
		}
		if len(res.confusions) > topConfusions {
			line("")
			line("%d more answer pairs in `requests.csv`.", len(res.confusions)-topConfusions)
		}
	}
	line("")
	line("## τ sweep")
	line("")
	line("| τ | Hits | Hit-rate | False hits | FP rate | Correct-hit rate | Unseen-text hits (correct) | Net savings | p50 with cache | p95 with cache |")
	line("|---|---|---|---|---|---|---|---|---|---|")
	for _, sp := range res.sweep {
		line("| %s | %d | %s | %d | %s | %s | %d (%d) | %s | %s | %s |", tauText(sp.Tau), sp.Hits, percent(sp.HitRate()), sp.FalseHits, percent(sp.FPRate()), percent(sp.CorrectHitRate()), sp.NovelHits, sp.NovelCorrectHits(), dollars6(sp.NetUSD()), millisUnit(sp.P50), millisUnit(sp.P95))
	}
	return bw.Flush()
}

func (res result) writeBrief(w io.Writer) error {
	o, b, p := res.opts, res.baseline, res.chosen
	_, err := fmt.Fprintf(w, `corpus: %s (%d prompts, %d answers)
traffic: %d requests, zipf %g, seed %d; eligible %d
embedder: %s
τ = %s: hit-rate %s (%d/%d) with false-positive rate %s (%d/%d); hits on text not seen before %d, correct %d; exact-match hit-rate %s
money: net savings $%s = avoided by correct hits $%s − embedding toll $%s; false hits skip $%s (not savings); break-even correct-hit rate %s
latency p50/p95: without cache %s / %s, with cache %s / %s
outputs: %s
synthetic corpus, lexical local embedder, mock-model latencies and flag prices: not a production measurement
`,
		o.corpus, len(res.replay.Corpus.Prompts), len(res.replay.Corpus.Answers),
		o.requests, o.zipf, o.seed, b.Eligible,
		res.replay.Embedder,
		tauText(o.tau), percent(p.HitRate()), p.Hits, p.Eligible, percent(p.FPRate()), p.FalseHits, p.Hits, p.NovelHits, p.NovelCorrectHits(), percent(ratio(b.Repeats, b.Eligible)),
		dollars6(p.NetUSD()), dollars6(p.AvoidedUSD), dollars6(p.TollUSD), dollars6(p.WrongHitUSD), percent(b.BreakEven()),
		millisUnit(b.P50), millisUnit(b.P95), millisUnit(p.P50), millisUnit(p.P95),
		strings.Join([]string{o.out + "/sweep.csv", o.out + "/requests.csv", o.out + "/summary.md"}, ", "),
	)
	return err
}

func ratio(num, den int) float64 {
	if den == 0 {
		return math.NaN()
	}
	return float64(num) / float64(den)
}

func tauText(v float64) string { return strconv.FormatFloat(v, 'f', -1, 64) }

func rate(v float64) string {
	if math.IsNaN(v) {
		return "undefined"
	}
	return strconv.FormatFloat(v, 'f', 6, 64)
}

func percent(v float64) string {
	if math.IsNaN(v) {
		return "undefined"
	}
	return strconv.FormatFloat(100*v, 'f', 2, 64) + "%"
}

func dollars(v float64) string { return strconv.FormatFloat(v, 'f', 9, 64) }

func dollars6(v float64) string { return strconv.FormatFloat(v, 'f', 6, 64) }

func millis(d time.Duration) string {
	return strconv.FormatFloat(float64(d)/float64(time.Millisecond), 'f', 3, 64)
}

func millisUnit(d time.Duration) string { return millis(d) + " ms" }
