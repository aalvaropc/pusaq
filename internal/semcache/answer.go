package semcache

import (
	"crypto/sha256"
	"encoding/hex"
	"hash"
	"io"
)

// MaxToolCalls bounds the tool-call indices an AnswerDigest tracks.
const MaxToolCalls = 128

// AnswerDigest fingerprints an answer's text and tool calls from fragments.
type AnswerDigest struct {
	text     hash.Hash
	calls    []*callDigest
	overflow bool
}

type callDigest struct {
	name, arguments hash.Hash
}

// Text adds a fragment of the answer's text.
func (d *AnswerDigest) Text(fragment string) {
	if d.text == nil {
		d.text = sha256.New()
	}
	_, _ = io.WriteString(d.text, fragment)
}

// ToolCall adds fragments of the name and arguments of the call at index.
func (d *AnswerDigest) ToolCall(index int, name, arguments string) {
	if index < 0 || index >= MaxToolCalls {
		d.overflow = true
		return
	}
	for len(d.calls) <= index {
		d.calls = append(d.calls, nil)
	}
	c := d.calls[index]
	if c == nil {
		c = &callDigest{name: sha256.New(), arguments: sha256.New()}
		d.calls[index] = c
	}
	_, _ = io.WriteString(c.name, name)
	_, _ = io.WriteString(c.arguments, arguments)
}

// Bounded reports whether every tool-call index was below MaxToolCalls.
func (d *AnswerDigest) Bounded() bool { return !d.overflow }

// Fingerprint identifies the answer; without tool calls it is Fingerprint(text).
func (d *AnswerDigest) Fingerprint() string {
	var text []byte
	if d.text == nil {
		sum := sha256.Sum256(nil)
		text = sum[:]
	} else {
		text = d.text.Sum(nil)
	}
	if len(d.calls) == 0 {
		return hex.EncodeToString(text)[:16]
	}
	h := sha256.New()
	_, _ = h.Write(text)
	for _, c := range d.calls {
		if c == nil {
			continue
		}
		_, _ = h.Write(c.name.Sum(nil))
		_, _ = h.Write(c.arguments.Sum(nil))
	}
	return hex.EncodeToString(h.Sum(nil))[:16]
}
