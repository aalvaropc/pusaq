package policy

import (
	"slices"
	"strings"
	"testing"
)

var (
	benchEN = []string{
		"Can you review this Go handler and explain why the p99 latency doubles when the connection pool is exhausted? The service runs 3 replicas behind a load balancer on port 8080, and each replica opens at most 50 connections to PostgreSQL 16.",
		"Summarize the trade-offs between optimistic and pessimistic locking for an inventory service that processes about 1200 orders per minute, and suggest which isolation level fits a checkout flow with two writes per order.",
		"Rewrite the following paragraph so it reads more clearly for a non-technical audience: the cache stores responses keyed by tenant and model, and a lookup that misses falls through to the provider, which adds roughly 400 ms.",
		"List the steps to migrate a Terraform module from one state backend to another without downtime, including how to verify that the resource count before and after the migration matches.",
	}
	benchES = []string{
		"¿Puedes revisar este manejador en Go y explicar por qué la latencia p99 se duplica cuando se agota el pool de conexiones? El servicio corre 3 réplicas detrás de un balanceador en el puerto 8080 y cada réplica abre como máximo 50 conexiones a PostgreSQL 16.",
		"Resume las ventajas y desventajas del bloqueo optimista y pesimista para un servicio de almacén que procesa unas 1200 órdenes por minuto, y sugiere qué nivel de aislamiento conviene a un flujo de pago con dos escrituras por orden.",
		"Reescribe el siguiente párrafo para que lo entienda un público no técnico: la caché guarda respuestas por inquilino y modelo, y una búsqueda que falla pasa al proveedor, lo que añade unos 400 ms.",
		"Enumera los pasos para migrar un módulo de Terraform de un backend de estado a otro sin interrupciones, incluyendo cómo comprobar que el número de recursos coincide antes y después de la migración.",
	}
)

// repeatTo cycles through paragraphs until the prompt holds at least n bytes.
func repeatTo(paragraphs []string, n int) string {
	var b strings.Builder
	for i := 0; b.Len() < n; i++ {
		if i > 0 {
			b.WriteString("\n\n")
		}
		b.WriteString(paragraphs[i%len(paragraphs)])
	}
	return b.String()
}

var benchCues = []string{
	"xtodayx xcurrentx xhoyx xtonightx xyesterdayx xlast nightx xayerx xthis weekx xeste mesx xlatestx xpricex xactualx xpreciox xis itx",
	"xignore xprevious xinstructionx xignora xinstrucciones xanterioresx xyou are nowx xno xahora eresx xsin xsystem xpromptx xrevealx xprompt xrevelx xnew instrucx:",
	"x@x +12 34 (1x 1-1x 987 654 321x xtelx 12345678x 1234 5678 9012 3456 GB12 ABCD 1234 5678 9012 34 xsk-x xAKIAx xghp_x -----BEGIN x x.eyJx xbearer xtokenx=",
}

// BenchmarkClassify measures the content rules over prompts that match none
// of them; the cue prompt passes every prefilter, so every pattern runs.
func BenchmarkClassify(b *testing.B) {
	for _, bc := range []struct {
		name, text string
	}{
		{"en/short", benchEN[0]},
		{"es/short", benchES[0]},
		{"en/4KiB", repeatTo(benchEN, 4096)},
		{"es/4KiB", repeatTo(benchES, 4096)},
		{"cues/4KiB", repeatTo(benchCues, 4096)},
	} {
		b.Run(bc.name, func(b *testing.B) {
			if f := Classify(bc.text); !f.Empty() {
				b.Fatalf("benchmark prompt matches %v", f.List())
			}
			if bc.name == "cues/4KiB" {
				s := scan{text: bc.text}
				for _, r := range slices.Concat(sensitiveRules, injectionRules, temporalMasks, temporalRules) {
					if !r.gate(&s) {
						b.Fatalf("cue prompt skips a %q pattern", r.kind)
					}
				}
			}
			b.SetBytes(int64(len(bc.text)))
			b.ReportAllocs()
			for b.Loop() {
				Classify(bc.text)
			}
		})
	}
}
