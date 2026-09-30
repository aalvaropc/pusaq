// Package policy holds the content rules of the policy filter: small,
// stated heuristics that flag sensitive data, prompt-injection phrasing,
// and time-dependent questions in message content. Every rule is a pattern
// with known limits, measured against a labeled corpus
// (docs/measurements/content-rules.md). It is a policy filter, not a
// firewall: regex and keyword heuristics do not promise to catch every
// attack or every identifier (constitution rule 8).
//
// Flags name what kind of thing matched — never the matched text — so
// they can travel into cost events and denial messages without leaking the
// very content they describe.
package policy

import (
	"regexp"
	"slices"
	"strings"
)

// Flags is the outcome of classifying a request's content: the kinds
// matched per family, each sorted and without duplicates. Empty slices
// mean nothing matched.
type Flags struct {
	// Sensitive lists personal identifiers and secrets: pii:email,
	// pii:phone, pii:card, pii:iban, secret:api_key, secret:private_key,
	// secret:jwt, secret:bearer, secret:assignment.
	Sensitive []string
	// Injection lists prompt-injection phrasings:
	// injection:ignore_instructions, injection:role_override,
	// injection:prompt_leak, injection:role_marker.
	Injection []string
	// Temporal lists time-dependence cues: temporal:now,
	// temporal:relative_day, temporal:period, temporal:latest,
	// temporal:clock.
	Temporal []string
}

// Classify runs every family over the texts.
func Classify(texts ...string) Flags {
	return Flags{Sensitive: Sensitive(texts...), Injection: Injection(texts...), Temporal: Temporal(texts...)}
}

// Empty reports whether no rule matched.
func (f Flags) Empty() bool {
	return len(f.Sensitive) == 0 && len(f.Injection) == 0 && len(f.Temporal) == 0
}

// List concatenates the families in a stable order (sensitive, injection,
// temporal); nil when nothing matched.
func (f Flags) List() []string {
	if f.Empty() {
		return nil
	}
	out := make([]string, 0, len(f.Sensitive)+len(f.Injection)+len(f.Temporal))
	out = append(out, f.Sensitive...)
	out = append(out, f.Injection...)
	out = append(out, f.Temporal...)
	return out
}

// RE2 has ASCII-only \b, so alternations that start or end in a non-ASCII
// letter (Spanish accents) use explicit Unicode boundaries instead.
const (
	ws = `(?:^|[^\pL\pN_])`
	we = `(?:$|[^\pL\pN_])`
)

// rule is one pattern that, when it matches, contributes a kind. check,
// when set, must accept the match for it to count (checksums, digit
// counts) — the honest half of "regex plus heuristics".
type rule struct {
	kind  string
	re    *regexp.Regexp
	check func(match string) bool
}

func (r rule) hit(text string) bool {
	if r.check == nil {
		return r.re.MatchString(text)
	}
	for _, m := range r.re.FindAllString(text, -1) {
		if r.check(m) {
			return true
		}
	}
	return false
}

func kinds(rules []rule, texts []string) []string {
	var out []string
	for _, r := range rules {
		for _, t := range texts {
			if r.hit(t) {
				out = append(out, r.kind)
				break
			}
		}
	}
	if len(out) == 0 {
		return nil
	}
	slices.Sort(out)
	return slices.Compact(out)
}

// --- Sensitive: personal identifiers and secrets --------------------------

var sensitiveRules = []rule{
	{kind: "pii:email", re: regexp.MustCompile(`(?i)\b[a-z0-9._%+-]+@[a-z0-9-]+(?:\.[a-z0-9-]+)*\.[a-z]{2,}\b`)},
	// International numbers: dots are never a separator (IPs, versions).
	{kind: "pii:phone", re: regexp.MustCompile(`\+\d{1,3}[\s-]?(?:\(\d{1,4}\)[\s-]?)?\d{1,4}(?:[\s-]?\d{2,4}){1,4}`), check: digitsBetween(8, 15)},
	{kind: "pii:phone", re: regexp.MustCompile(`\(\d{1,4}\)[\s-]?\d{3,4}[\s-]?\d{3,4}\b`)},
	{kind: "pii:phone", re: regexp.MustCompile(`\b\d{2,4}-\d{3,4}-\d{4}\b`)},
	// Peruvian mobiles: 9xx xxx xxx.
	{kind: "pii:phone", re: regexp.MustCompile(`(?:^|[^\w+])9\d{2}[\s-]\d{3}[\s-]\d{3}(?:$|[^\w-])`)},
	// A bare digit run only counts next to a phone cue; "number" alone is
	// deliberately not one (order numbers, IDs).
	{kind: "pii:phone", re: regexp.MustCompile(`(?i)\b(?:tel(?:[ée]fono|f)?|cel(?:ular)?|phone|whatsapp|wsp|m[óo]vil|ll[áa]ma(?:me)?|call\s+me)\b\D{0,12}\d{8,12}\b`)},
	{kind: "pii:card", re: regexp.MustCompile(`\b(?:\d[ -]?){12,18}\d\b`), check: cardNumber},
	{kind: "pii:iban", re: regexp.MustCompile(`\b[A-Z]{2}\d{2}(?: ?[A-Z0-9]{4}){2,7}(?: ?[A-Z0-9]{1,4})?\b`), check: func(m string) bool {
		return ibanValid(strings.ReplaceAll(m, " ", ""))
	}},
	{kind: "secret:api_key", re: regexp.MustCompile(`\bsk-[A-Za-z0-9_-]{20,}`)},
	{kind: "secret:api_key", re: regexp.MustCompile(`\bAKIA[0-9A-Z]{16}\b`)},
	{kind: "secret:api_key", re: regexp.MustCompile(`\b(?:gh[pousr]_[A-Za-z0-9]{36,}|github_pat_[A-Za-z0-9_]{22,})`)},
	{kind: "secret:private_key", re: regexp.MustCompile(`-----BEGIN (?:[A-Z]+ )?PRIVATE KEY-----`)},
	{kind: "secret:jwt", re: regexp.MustCompile(`\beyJ[A-Za-z0-9_-]{8,}\.eyJ[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{16,}`)},
	{kind: "secret:bearer", re: regexp.MustCompile(`(?i)\bbearer\s+[A-Za-z0-9._~+/-]{20,}=*`)},
	{kind: "secret:assignment", re: regexp.MustCompile(`(?i)\b(?:api[_-]?key|secret|password|passwd|token)\s*[:=]\s*["']?[A-Za-z0-9_./+-]{16,}`)},
}

// Sensitive reports the kinds of personal identifiers and secrets found in
// the texts. Stated limits: unformatted or dot-separated phone numbers
// without a cue are missed; short passwords are missed; only the secret
// shapes listed are known.
func Sensitive(texts ...string) []string { return kinds(sensitiveRules, texts) }

func digitsBetween(lo, hi int) func(string) bool {
	return func(m string) bool {
		n := digitCount(m)
		return n >= lo && n <= hi
	}
}

func digitCount(s string) int {
	n := 0
	for _, r := range s {
		if r >= '0' && r <= '9' {
			n++
		}
	}
	return n
}

func onlyDigits(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r >= '0' && r <= '9' {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// cardNumber accepts a 13–19 digit run with a known issuer prefix and a
// valid Luhn checksum — what keeps timestamps, tracking codes and random
// digit runs out.
func cardNumber(m string) bool {
	d := onlyDigits(m)
	if len(d) < 13 || len(d) > 19 {
		return false
	}
	switch {
	case d[0] == '4':
	case d[0] == '5' && d[1] >= '1' && d[1] <= '5':
	case d[0] == '2' && d[1] >= '2' && d[1] <= '7':
	case d[0] == '3' && (d[1] == '4' || d[1] == '7'):
	case strings.HasPrefix(d, "6011"), strings.HasPrefix(d, "65"):
	case d[0] == '6' && d[1] == '4' && d[2] >= '4' && d[2] <= '9':
	default:
		return false
	}
	return luhn(d)
}

func luhn(digits string) bool {
	sum := 0
	double := false
	for i := len(digits) - 1; i >= 0; i-- {
		n := int(digits[i] - '0')
		if double {
			n *= 2
			if n > 9 {
				n -= 9
			}
		}
		sum += n
		double = !double
	}
	return sum%10 == 0
}

// ibanValid checks length and the ISO 13616 mod-97 checksum of a
// space-free IBAN.
func ibanValid(iban string) bool {
	if len(iban) < 15 || len(iban) > 34 {
		return false
	}
	rearranged := iban[4:] + iban[:4]
	mod := 0
	for _, r := range rearranged {
		var v int
		switch {
		case r >= '0' && r <= '9':
			v = int(r - '0')
		case r >= 'A' && r <= 'Z':
			v = int(r-'A') + 10
		default:
			return false
		}
		if v >= 10 {
			mod = (mod*100 + v) % 97
		} else {
			mod = (mod*10 + v) % 97
		}
	}
	return mod == 1
}

// --- Injection: phrasings that try to override the instructions ----------

var injectionRules = []rule{
	{kind: "injection:ignore_instructions", re: regexp.MustCompile(`(?i)\b(?:ignore|disregard|forget|override|bypass)\s+(?:all\s+|any\s+|the\s+|your\s+|these\s+|those\s+)*(?:previous|prior|above|earlier|preceding|initial|original|system)\s+(?:instructions?|prompts?|rules?|directions?|guidelines?|constraints?)\b`)},
	{kind: "injection:ignore_instructions", re: regexp.MustCompile(`(?i)` + ws + `(?:ignor[aá]|olvid[aá]|omite|descarta|desobedece|s[aá]ltate)\s+(?:todas?\s+|cualquier\s+)?(?:las?\s+|los\s+|tus\s+|sus\s+|estas\s+)?(?:instrucciones|reglas|indicaciones|[óo]rdenes|directrices|restricciones|prompts?)\s+(?:anteriores|previas|previos|iniciales|originales|del\s+sistema|de\s+arriba)` + we)}, //nolint:misspell // Spanish
	{kind: "injection:role_override", re: regexp.MustCompile(`(?i)\b(?:you\s+are\s+now|from\s+now\s+on,?\s+you(?:'re|\s+are)|pretend\s+(?:that\s+)?you\s+(?:are|have)|act\s+as\s+if\s+you\s+(?:are|were|have|had))\b.{0,40}\b(?:no\s+(?:rules|restrictions|limits|filters|guidelines)|without\s+(?:any\s+)?(?:rules|restrictions|limits|filters)|unrestricted|unfiltered|jailbroken|developer\s+mode|DAN)\b`)},
	{kind: "injection:role_override", re: regexp.MustCompile(`(?i)` + ws + `(?:ahora\s+eres|a\s+partir\s+de\s+ahora\s+eres|desde\s+ahora\s+eres|act[úu]a|comp[óo]rtate|finge|haz)\b.{0,40}` + ws + `(?:como\s+si\s+no\s+(?:tuvieras|tuvieses|hubiera|existieran)\s+(?:reglas|restricciones|l[íi]mites|filtros)|sin\s+(?:reglas|restricciones|l[íi]mites|filtros|censura)|ilimitad[oa]|sin\s+ning[úu]na\s+restricci[óo]n)` + we)},
	{kind: "injection:prompt_leak", re: regexp.MustCompile(`(?i)\b(?:reveal|print|show|display|repeat|output|leak|dump|tell\s+me|what\s+is|give\s+me|write\s+out)\b.{0,30}\b(?:(?:your|its)\s+(?:system|initial|hidden|secret|original)\s+(?:prompt|instructions?|message)|the\s+(?:system|initial|hidden)\s+prompt)\b`)},
	{kind: "injection:prompt_leak", re: regexp.MustCompile(`(?i)` + ws + `(?:revel[aá]|muestr[aá]|imprim[eí]|repit[eí]|repet[ií]|dime|cu[áa]l\s+es|dame|escrib[eí])\b.{0,30}\b(?:tu|tus|su|sus|el|las?)\s+(?:prompt\s+del?\s+sistema|system\s+prompt|instrucciones\s+(?:del\s+sistema|iniciales|ocultas|secretas|originales)|mensaje\s+del?\s+sistema|prompt\s+(?:inicial|oculto|secreto))` + we)}, //nolint:misspell // Spanish
	{kind: "injection:role_marker", re: regexp.MustCompile(`(?i)(?:<\|im_start\|>\s*system|<\|system\|>|\[(?:system|inst)\]|(?:^|\n)\s*(?:###\s*)?(?:new|nuevas)\s+(?:instructions?|instrucciones)\s*:)`)},
}

// Injection reports the prompt-injection phrasings found in the texts.
// Stated limits: it is lexical — paraphrases without these lexemes pass;
// a developer asking to "print the system prompt" is flagged; a user
// correcting their own earlier message is not.
func Injection(texts ...string) []string { return kinds(injectionRules, texts) }

// --- Temporal: the answer depends on when the question is asked ----------

// Masks remove idioms that contain a cue without meaning "now" ("today's
// world", "currently selected", "por la mañana", "hoy en día") before the
// cues run — RE2 has no lookaround, so this is how the exceptions are stated.
var temporalMasks = []*regexp.Regexp{
	regexp.MustCompile(`(?i)(?:\busa\s+today\b|\.today\b|\btoday\s*\(|\btoday'?s\s+(?:world|society|market|economy|business(?:es)?|standards?|age|era|landscape|environment|workplace|technology)\b|\bcurrently\s+(?:selected|logged|active|focused|running|installed|mounted|checked|opened?|displayed|loaded|executing)\b|\bcurrent\s+(?:directory|folder|file|branch|user|session|thread|context|line|element|tab|window|value|state|density|flow|loop)\b)`),
	regexp.MustCompile(`(?i)` + ws + `(?:hoy\s+(?:en\s+)?d[íi]a|(?:por|de|en|durante|cada|toda)\s+la\s+mañana|todas\s+las\s+mañanas|actualmente\s+(?:seleccionad[oa]|logueado|activ[oa]|conectad[oa]|abiert[oa])|ahora\s+bien|por\s+ahora)` + we),
}

var temporalRules = []rule{
	{kind: "temporal:now", re: regexp.MustCompile(`(?i)\b(?:today|tonight|right\s+now|as\s+of\s+(?:now|today)|at\s+(?:the|this)\s+moment|at\s+this\s+time|currently)\b`)},
	{kind: "temporal:now", re: regexp.MustCompile(`(?i)` + ws + `(?:hoy|esta\s+noche|ahora\s+mismo|en\s+este\s+momento|en\s+estos\s+momentos|actualmente|al\s+d[íi]a\s+de\s+hoy|a\s+la\s+fecha)` + we)}, //nolint:misspell // Spanish
	{kind: "temporal:relative_day", re: regexp.MustCompile(`(?i)\b(?:yesterday|tomorrow|the\s+day\s+after\s+tomorrow|last\s+(?:night|week|month|year)|next\s+(?:week|month|year|monday|tuesday|wednesday|thursday|friday|saturday|sunday))\b`)},
	{kind: "temporal:relative_day", re: regexp.MustCompile(`(?i)` + ws + `(?:ayer|anteayer|mañana|pasado\s+mañana|anoche|la\s+semana\s+pasada|el\s+mes\s+pasado|el\s+a[ñn]o\s+pasado|la\s+(?:pr[óo]xima\s+semana|semana\s+que\s+viene)|el\s+pr[óo]ximo\s+(?:mes|a[ñn]o|lunes|martes|mi[ée]rcoles|jueves|viernes|s[áa]bado|domingo))` + we)},
	{kind: "temporal:period", re: regexp.MustCompile(`(?i)\bthis\s+(?:week|month|year|weekend|morning|afternoon|evening|quarter|season)\b`)},
	{kind: "temporal:period", re: regexp.MustCompile(`(?i)` + ws + `est[ea]\s+(?:semana|mes|a[ñn]o|fin\s+de\s+semana|tarde|mañana|trimestre|temporada)` + we)},
	{kind: "temporal:latest", re: regexp.MustCompile(`(?i)\b(?:latest|current|most\s+recent|up[- ]to[- ]date|live|real[- ]time|newest)\s+(?:[\pL\pN]+\s+){0,2}(?:prices?|cost|(?:exchange|interest|inflation|mortgage)\s+rates?|rate\s+of\s+the\s+\w+|quote|version|release|news|headlines|weather|forecast|temperature|scores?|standings|results|status|balance|availability|stock\s+price|market\s+cap|schedule|hours)\b`)},
	{kind: "temporal:latest", re: regexp.MustCompile(`(?i)` + ws + `(?:(?:[úu]ltim[oa]s?|actual(?:es)?|m[áa]s\s+recientes?|en\s+tiempo\s+real|en\s+vivo)\s+(?:[\pL\pN]+\s+){0,2}(?:precios?|cotizaci[óo]n|tipo\s+de\s+cambio|tasa\s+de\s+[\pL]+|versi[óo]n|noticias|titulares|clima|pron[óo]stico|temperatura|resultados?|marcador|tabla\s+de\s+posiciones|horario|disponibilidad|saldo)|(?:precios?|cotizaci[óo]n|tipo\s+de\s+cambio|versi[óo]n|noticias|clima|temperatura|resultados?|marcador|saldo|horario|d[óo]lar)\s+(?:actual(?:es)?|m[áa]s\s+recientes?|de\s+hoy|de\s+ahora|del\s+momento|al\s+d[íi]a\s+de\s+hoy))` + we)}, //nolint:misspell // Spanish
	{kind: "temporal:clock", re: regexp.MustCompile(`(?i)\b(?:what\s+(?:time|day|date|year)\s+is\s+it|what'?s\s+(?:today'?s\s+date|the\s+(?:date|time))|qu[ée]\s+(?:hora|d[íi]a|fecha)\s+es|en\s+qu[ée]\s+a[ñn]o\s+estamos)\b`)},
}

// Temporal reports the time-dependence cues found in the texts. Stated
// limits: it is cue-based — "who is the president of Peru?" carries no cue
// and passes; bare "now"/"ahora" are discourse markers and never count;
// "current" counts only next to a volatile noun.
func Temporal(texts ...string) []string {
	masked := make([]string, len(texts))
	for i, t := range texts {
		for _, m := range temporalMasks {
			t = m.ReplaceAllString(t, " ")
		}
		masked[i] = t
	}
	return kinds(temporalRules, masked)
}
