package policy

import (
	"regexp"
	"slices"
	"strings"
)

const (
	refWS = `(?:^|[^\pL\pN_])`
	refWE = `(?:$|[^\pL\pN_])`
)

type refRule struct {
	kind  string
	re    *regexp.Regexp
	check func(match string) bool
}

func (r refRule) hit(text string) bool {
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

func refKinds(rules []refRule, texts []string) []string {
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

func refTemporal(texts ...string) []string {
	masked := make([]string, len(texts))
	for i, t := range texts {
		for _, m := range refTemporalMasks {
			t = m.ReplaceAllString(t, " ")
		}
		masked[i] = t
	}
	return refKinds(refTemporalRules, masked)
}

func refClassify(texts ...string) Flags {
	return Flags{
		Sensitive: refKinds(refSensitiveRules, texts),
		Injection: refKinds(refInjectionRules, texts),
		Temporal:  refTemporal(texts...),
	}
}

var refSensitiveRules = []refRule{
	{kind: "pii:email", re: regexp.MustCompile(`(?i)\b[a-z0-9._%+-]+@[a-z0-9-]+(?:\.[a-z0-9-]+)*\.[a-z]{2,}\b`)},
	{kind: "pii:phone", re: regexp.MustCompile(`\+\d{1,3}[\s-]?(?:\(\d{1,4}\)[\s-]?)?\d{1,4}(?:[\s-]?\d{2,4}){1,4}`), check: digitsBetween(8, 15)},
	{kind: "pii:phone", re: regexp.MustCompile(`\(\d{1,4}\)[\s-]?\d{3,4}[\s-]?\d{3,4}\b`)},
	{kind: "pii:phone", re: regexp.MustCompile(`\b\d{2,4}-\d{3,4}-\d{4}\b`)},
	{kind: "pii:phone", re: regexp.MustCompile(`(?:^|[^\w+])9\d{2}[\s-]\d{3}[\s-]\d{3}(?:$|[^\w-])`)},
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

var refInjectionRules = []refRule{
	{kind: "injection:ignore_instructions", re: regexp.MustCompile(`(?i)\b(?:ignore|disregard|forget|override|bypass)\s+(?:all\s+|any\s+|the\s+|your\s+|these\s+|those\s+)*(?:previous|prior|above|earlier|preceding|initial|original|system)\s+(?:instructions?|prompts?|rules?|directions?|guidelines?|constraints?)\b`)},
	{kind: "injection:ignore_instructions", re: regexp.MustCompile(`(?i)` + refWS + `(?:ignor[aá]|olvid[aá]|omite|descarta|desobedece|s[aá]ltate)\s+(?:todas?\s+|cualquier\s+)?(?:las?\s+|los\s+|tus\s+|sus\s+|estas\s+)?(?:instrucciones|reglas|indicaciones|[óo]rdenes|directrices|restricciones|prompts?)\s+(?:anteriores|previas|previos|iniciales|originales|del\s+sistema|de\s+arriba)` + refWE)}, //nolint:misspell
	{kind: "injection:role_override", re: regexp.MustCompile(`(?i)\b(?:you\s+are\s+now|from\s+now\s+on,?\s+you(?:'re|\s+are)|pretend\s+(?:that\s+)?you\s+(?:are|have)|act\s+as\s+if\s+you\s+(?:are|were|have|had))\b.{0,40}\b(?:no\s+(?:rules|restrictions|limits|filters|guidelines)|without\s+(?:any\s+)?(?:rules|restrictions|limits|filters)|unrestricted|unfiltered|jailbroken|developer\s+mode|DAN)\b`)},
	{kind: "injection:role_override", re: regexp.MustCompile(`(?i)` + refWS + `(?:ahora\s+eres|a\s+partir\s+de\s+ahora\s+eres|desde\s+ahora\s+eres|act[úu]a|comp[óo]rtate|finge|haz)\b.{0,40}` + refWS + `(?:como\s+si\s+no\s+(?:tuvieras|tuvieses|hubiera|existieran)\s+(?:reglas|restricciones|l[íi]mites|filtros)|sin\s+(?:reglas|restricciones|l[íi]mites|filtros|censura)|ilimitad[oa]|sin\s+ning[úu]na\s+restricci[óo]n)` + refWE)},
	{kind: "injection:prompt_leak", re: regexp.MustCompile(`(?i)\b(?:reveal|print|show|display|repeat|output|leak|dump|tell\s+me|what\s+is|give\s+me|write\s+out)\b.{0,30}\b(?:(?:your|its)\s+(?:system|initial|hidden|secret|original)\s+(?:prompt|instructions?|message)|the\s+(?:system|initial|hidden)\s+prompt)\b`)},
	{kind: "injection:prompt_leak", re: regexp.MustCompile(`(?i)` + refWS + `(?:revel[aá]|muestr[aá]|imprim[eí]|repit[eí]|repet[ií]|dime|cu[áa]l\s+es|dame|escrib[eí])\b.{0,30}\b(?:tu|tus|su|sus|el|las?)\s+(?:prompt\s+del?\s+sistema|system\s+prompt|instrucciones\s+(?:del\s+sistema|iniciales|ocultas|secretas|originales)|mensaje\s+del?\s+sistema|prompt\s+(?:inicial|oculto|secreto))` + refWE)}, //nolint:misspell
	{kind: "injection:role_marker", re: regexp.MustCompile(`(?i)(?:<\|im_start\|>\s*system|<\|system\|>|\[(?:system|inst)\]|(?:^|\n)\s*(?:###\s*)?(?:new|nuevas)\s+(?:instructions?|instrucciones)\s*:)`)},
}

var refTemporalMasks = []*regexp.Regexp{
	regexp.MustCompile(`(?i)(?:\busa\s+today\b|\.today\b|\btoday\s*\(|\btoday'?s\s+(?:world|society|market|economy|business(?:es)?|standards?|age|era|landscape|environment|workplace|technology)\b|\bcurrently\s+(?:selected|logged|active|focused|running|installed|mounted|checked|opened?|displayed|loaded|executing)\b|\bcurrent\s+(?:directory|folder|file|branch|user|session|thread|context|line|element|tab|window|value|state|density|flow|loop)\b)`),
	regexp.MustCompile(`(?i)` + refWS + `(?:hoy\s+(?:en\s+)?d[íi]a|(?:por|de|en|durante|cada|toda)\s+la\s+mañana|todas\s+las\s+mañanas|actualmente\s+(?:seleccionad[oa]|logueado|activ[oa]|conectad[oa]|abiert[oa])|ahora\s+bien|por\s+ahora)` + refWE),
}

var refTemporalRules = []refRule{
	{kind: "temporal:now", re: regexp.MustCompile(`(?i)\b(?:today|tonight|right\s+now|as\s+of\s+(?:now|today)|at\s+(?:the|this)\s+moment|at\s+this\s+time|currently)\b`)},
	{kind: "temporal:now", re: regexp.MustCompile(`(?i)` + refWS + `(?:hoy|esta\s+noche|ahora\s+mismo|en\s+este\s+momento|en\s+estos\s+momentos|actualmente|al\s+d[íi]a\s+de\s+hoy|a\s+la\s+fecha)` + refWE)}, //nolint:misspell
	{kind: "temporal:relative_day", re: regexp.MustCompile(`(?i)\b(?:yesterday|tomorrow|the\s+day\s+after\s+tomorrow|last\s+(?:night|week|month|year)|next\s+(?:week|month|year|monday|tuesday|wednesday|thursday|friday|saturday|sunday))\b`)},
	{kind: "temporal:relative_day", re: regexp.MustCompile(`(?i)` + refWS + `(?:ayer|anteayer|mañana|pasado\s+mañana|anoche|la\s+semana\s+pasada|el\s+mes\s+pasado|el\s+a[ñn]o\s+pasado|la\s+(?:pr[óo]xima\s+semana|semana\s+que\s+viene)|el\s+pr[óo]ximo\s+(?:mes|a[ñn]o|lunes|martes|mi[ée]rcoles|jueves|viernes|s[áa]bado|domingo))` + refWE)},
	{kind: "temporal:period", re: regexp.MustCompile(`(?i)\bthis\s+(?:week|month|year|weekend|morning|afternoon|evening|quarter|season)\b`)},
	{kind: "temporal:period", re: regexp.MustCompile(`(?i)` + refWS + `est[ea]\s+(?:semana|mes|a[ñn]o|fin\s+de\s+semana|tarde|mañana|trimestre|temporada)` + refWE)},
	{kind: "temporal:latest", re: regexp.MustCompile(`(?i)\b(?:latest|current|most\s+recent|up[- ]to[- ]date|live|real[- ]time|newest)\s+(?:[\pL\pN]+\s+){0,2}(?:prices?|cost|(?:exchange|interest|inflation|mortgage)\s+rates?|rate\s+of\s+the\s+\w+|quote|version|release|news|headlines|weather|forecast|temperature|scores?|standings|results|status|balance|availability|stock\s+price|market\s+cap|schedule|hours)\b`)},
	{kind: "temporal:latest", re: regexp.MustCompile(`(?i)` + refWS + `(?:(?:[úu]ltim[oa]s?|actual(?:es)?|m[áa]s\s+recientes?|en\s+tiempo\s+real|en\s+vivo)\s+(?:[\pL\pN]+\s+){0,2}(?:precios?|cotizaci[óo]n|tipo\s+de\s+cambio|tasa\s+de\s+[\pL]+|versi[óo]n|noticias|titulares|clima|pron[óo]stico|temperatura|resultados?|marcador|tabla\s+de\s+posiciones|horario|disponibilidad|saldo)|(?:precios?|cotizaci[óo]n|tipo\s+de\s+cambio|versi[óo]n|noticias|clima|temperatura|resultados?|marcador|saldo|horario|d[óo]lar)\s+(?:actual(?:es)?|m[áa]s\s+recientes?|de\s+hoy|de\s+ahora|del\s+momento|al\s+d[íi]a\s+de\s+hoy))` + refWE)}, //nolint:misspell
	{kind: "temporal:clock", re: regexp.MustCompile(`(?i)\b(?:what\s+(?:time|day|date|year)\s+is\s+it|what'?s\s+(?:today'?s\s+date|the\s+(?:date|time))|qu[ée]\s+(?:hora|d[íi]a|fecha)\s+es|en\s+qu[ée]\s+a[ñn]o\s+estamos)\b`)},
}
