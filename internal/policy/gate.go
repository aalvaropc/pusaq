package policy

import (
	"bytes"
	"math/bits"
	"strings"
	"unicode"
	"unicode/utf8"
)

type scan struct {
	text     string
	folded   []byte
	grams    gramSet
	shape    uint8
	hasShape bool
}

type gramSet struct {
	bits  []uint64
	shift uint
}

func gram(a, b, c byte) uint32 {
	return (uint32(a)<<16 | uint32(b)<<8 | uint32(c)) * 0x9e3779b1
}

func gramsOf(b []byte) []uint32 {
	var out []uint32
	for i := 2; i < len(b); i++ {
		out = append(out, gram(b[i-2], b[i-1], b[i]))
	}
	return out
}

func (g *gramSet) holds(grams []uint32) bool {
	for _, h := range grams {
		h >>= g.shift
		if g.bits[h>>6]&(1<<(h&63)) == 0 {
			return false
		}
	}
	return true
}

func newScans(texts []string) []scan {
	scans := make([]scan, len(texts))
	for i, t := range texts {
		scans[i].text = t
	}
	return scans
}

func (s *scan) fold() ([]byte, *gramSet) {
	if s.grams.bits == nil {
		s.folded = appendFolded(make([]byte, 0, len(s.text)), s.text)
		size := 1 << 9
		for size < 4*len(s.folded) && size < 1<<16 {
			size <<= 1
		}
		s.grams = gramSet{bits: make([]uint64, size/64), shift: uint(32 - bits.TrailingZeros(uint(size)))}
		for i := 2; i < len(s.folded); i++ {
			h := gram(s.folded[i-2], s.folded[i-1], s.folded[i]) >> s.grams.shift
			s.grams.bits[h>>6] |= 1 << (h & 63)
		}
	}
	return s.folded, &s.grams
}

func (s *scan) digits() uint8 {
	if !s.hasShape {
		s.shape = digitShapes(s.text)
		s.hasShape = true
	}
	return s.shape
}

func appendFolded(dst []byte, s string) []byte {
	space := false
	for i := 0; i < len(s); {
		c := s[i]
		if c >= utf8.RuneSelf {
			r, n := utf8.DecodeRuneInString(s[i:])
			if r == utf8.RuneError && n == 1 {
				dst = append(dst, c)
			} else {
				dst = utf8.AppendRune(dst, foldRune(r))
			}
			i += n
			space = false
			continue
		}
		i++
		switch {
		case c == ' ' || c == '\t' || c == '\n' || c == '\f' || c == '\r':
			if !space {
				dst = append(dst, ' ')
			}
			space = true
			continue
		case 'A' <= c && c <= 'Z':
			c += 'a' - 'A'
		}
		dst = append(dst, c)
		space = false
	}
	return dst
}

func foldRune(r rune) rune {
	m := r
	for f := unicode.SimpleFold(r); f != r; f = unicode.SimpleFold(f) {
		m = min(m, f)
	}
	if 'A' <= m && m <= 'Z' {
		m += 'a' - 'A'
	}
	return m
}

const (
	plusDigit uint8 = 1 << iota
	parenDigit
	dashDigits
	peruMobile
	digitRun8
	cardChain
	ibanHead
)

func digitShapes(t string) uint8 {
	var m uint8
	run, chain := 0, 0
	for i := 0; i < len(t); i++ {
		c := t[i]
		if isDigit(c) {
			run++
			chain++
			if run == 8 {
				m |= digitRun8
			}
			if chain == 13 {
				m |= cardChain
			}
			if run == 2 && i >= 3 && isUpper(t[i-2]) && isUpper(t[i-3]) {
				m |= ibanHead
			}
			if run == 1 && c == '9' && peruShape(t[i:]) {
				m |= peruMobile
			}
			continue
		}
		next := i+1 < len(t) && isDigit(t[i+1])
		switch c {
		case ' ', '-':
			if run == 0 {
				chain = 0
			}
			if c == '-' && run > 0 && next {
				m |= dashDigits
			}
		case '+':
			chain = 0
			if next {
				m |= plusDigit
			}
		case '(':
			chain = 0
			if next {
				m |= parenDigit
			}
		default:
			chain = 0
		}
		run = 0
	}
	return m
}

func peruShape(t string) bool {
	const form = "ddds" + "ddds" + "ddd"
	if len(t) < len(form) {
		return false
	}
	for i := range len(form) {
		c := t[i]
		sep := c == '\t' || c == '\n' || c == '\f' || c == '\r' || c == ' ' || c == '-'
		if form[i] == 'd' && !isDigit(c) || form[i] == 's' && !sep {
			return false
		}
	}
	return true
}

func isDigit(c byte) bool { return '0' <= c && c <= '9' }

func isUpper(c byte) bool { return 'A' <= c && c <= 'Z' }

type gate func(*scan) bool

func folded(words ...string) gate {
	type key struct {
		text  []byte
		grams []uint32
	}
	keys := make([]key, len(words))
	for i, w := range words {
		f := appendFolded(nil, w)
		keys[i] = key{f, gramsOf(f)}
	}
	return func(s *scan) bool {
		f, g := s.fold()
		for _, k := range keys {
			if g.holds(k.grams) && bytes.Contains(f, k.text) {
				return true
			}
		}
		return false
	}
}

func exact(words ...string) gate {
	return func(s *scan) bool {
		for _, w := range words {
			if strings.Contains(s.text, w) {
				return true
			}
		}
		return false
	}
}

func shaped(bit uint8) gate {
	return func(s *scan) bool { return s.digits()&bit != 0 }
}

func all(gates ...gate) gate {
	return func(s *scan) bool {
		for _, g := range gates {
			if !g(s) {
				return false
			}
		}
		return true
	}
}

func either(gates ...gate) gate {
	return func(s *scan) bool {
		for _, g := range gates {
			if g(s) {
				return true
			}
		}
		return false
	}
}

func phrases(heads []string, tails ...string) gate {
	var joined []string
	for _, h := range heads {
		for _, t := range tails {
			joined = append(joined, h+t)
		}
	}
	return all(folded(heads...), folded(joined...))
}
