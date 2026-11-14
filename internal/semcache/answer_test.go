package semcache

import "testing"

type call struct {
	index           int
	name, arguments string
}

func digest(text []string, calls []call) *AnswerDigest {
	d := &AnswerDigest{}
	for _, t := range text {
		d.Text(t)
	}
	for _, c := range calls {
		d.ToolCall(c.index, c.name, c.arguments)
	}
	return d
}

func TestAnswerDigestTextOnlyKeepsTheTextFingerprint(t *testing.T) {
	for _, text := range []string{"", "Hello world"} {
		if got := digest([]string{text}, nil).Fingerprint(); got != Fingerprint(text) {
			t.Fatalf("fingerprint(%q) = %s, want %s", text, got, Fingerprint(text))
		}
	}
	if got := (&AnswerDigest{}).Fingerprint(); got != Fingerprint("") {
		t.Fatalf("empty digest = %s, want the empty text's fingerprint", got)
	}
	if got := digest([]string{"Hello ", "world"}, nil).Fingerprint(); got != Fingerprint("Hello world") {
		t.Fatal("text fragmentation changes the fingerprint")
	}
}

func TestAnswerDigestCoversToolCalls(t *testing.T) {
	whole := digest(nil, []call{{0, "get_weather", `{"city":"Lima"}`}}).Fingerprint()
	fragmented := digest(nil, []call{{0, "get_", ""}, {0, "weather", `{"ci`}, {0, "", `ty":"Li`}, {0, "", `ma"}`}}).Fingerprint()
	if whole != fragmented {
		t.Fatal("tool-call fragmentation changes the fingerprint")
	}
	interleaved := digest(nil, []call{{0, "a", `{"x`}, {1, "b", `{"y`}, {0, "", `":1}`}, {1, "", `":2}`}}).Fingerprint()
	sequential := digest(nil, []call{{0, "a", `{"x":1}`}, {1, "b", `{"y":2}`}}).Fingerprint()
	if interleaved != sequential {
		t.Fatal("interleaved tool-call fragments change the fingerprint")
	}
	for name, other := range map[string]*AnswerDigest{
		"no call":         digest(nil, nil),
		"other arguments": digest(nil, []call{{0, "get_weather", `{"city":"Cusco"}`}}),
		"other function":  digest(nil, []call{{0, "get_forecast", `{"city":"Lima"}`}}),
		"text answer":     digest([]string{`get_weather{"city":"Lima"}`}, nil),
		"bytes shifted":   digest(nil, []call{{0, "get_weather{", `"city":"Lima"}`}}),
		"extra call":      digest(nil, []call{{0, "get_weather", `{"city":"Lima"}`}, {1, "get_weather", `{"city":"Lima"}`}}),
	} {
		if other.Fingerprint() == whole {
			t.Fatalf("%s shares the fingerprint of the tool call", name)
		}
	}
}

func TestAnswerDigestBoundsIndices(t *testing.T) {
	d := digest(nil, []call{{MaxToolCalls - 1, "a", "{}"}})
	if !d.Bounded() || len(d.calls) != MaxToolCalls {
		t.Fatalf("bounded = %v with %d slots, want the last index accepted", d.Bounded(), len(d.calls))
	}
	for _, index := range []int{-1, MaxToolCalls, 1 << 30} {
		d := digest(nil, []call{{index, "a", "{}"}})
		if d.Bounded() || len(d.calls) != 0 {
			t.Fatalf("index %d: bounded = %v with %d slots, want it refused", index, d.Bounded(), len(d.calls))
		}
	}
}
