package embed

import (
	"context"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func norm(t *testing.T, v []float32) float64 {
	t.Helper()
	var sum float64
	for _, x := range v {
		sum += float64(x) * float64(x)
	}
	return math.Sqrt(sum)
}

func TestLocalDeterministicAndNormalized(t *testing.T) {
	l := Local{}
	a1, _ := l.Embed(context.Background(), "how do I cancel my subscription")
	a2, _ := l.Embed(context.Background(), "how do I cancel my subscription")
	if sim := Cosine(a1, a2); math.Abs(sim-1) > 1e-6 {
		t.Fatalf("identical texts similarity = %g, want ~1", sim)
	}
	if n := norm(t, a1); math.Abs(n-1) > 1e-6 {
		t.Fatalf("|v| = %g, want 1 (L2-normalized)", n)
	}
	if len(a1) != LocalDim {
		t.Fatalf("dim = %d, want %d", len(a1), LocalDim)
	}
}

func TestLocalOverlapBeatsDisjoint(t *testing.T) {
	l := Local{}
	base, _ := l.Embed(context.Background(), "how do I cancel my subscription")
	overlap, _ := l.Embed(context.Background(), "how do I cancel my subscription please")
	disjoint, _ := l.Embed(context.Background(), "zzzz qqqq wwww kkkk")

	simOverlap := Cosine(base, overlap)
	simDisjoint := Cosine(base, disjoint)
	if simOverlap <= simDisjoint {
		t.Fatalf("overlap %g <= disjoint %g", simOverlap, simDisjoint)
	}
	if simOverlap < 0.8 {
		t.Fatalf("near-identical texts similarity = %g, want high", simOverlap)
	}
}

func TestLocalShortTextDoesNotPanic(t *testing.T) {
	l := Local{}
	if _, err := l.Embed(context.Background(), "hi"); err != nil {
		t.Fatal(err)
	}
	if _, err := l.Embed(context.Background(), ""); err != nil {
		t.Fatal(err)
	}
}

func TestHTTPEmbedRequestShapeAndAuth(t *testing.T) {
	var gotPath, gotAuth, gotBody string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		_, _ = io.WriteString(w, `{"data":[{"embedding":[3,4]}]}`)
	}))
	defer upstream.Close()

	e := NewHTTP("gemini-embed", upstream.URL+"/v1beta/openai", "gemini-embedding-001", "sk-e", time.Second)
	vec, err := e.Embed(context.Background(), "hello")
	if err != nil {
		t.Fatal(err)
	}
	if gotPath != "/v1beta/openai/embeddings" || gotAuth != "Bearer sk-e" {
		t.Fatalf("path=%q auth=%q", gotPath, gotAuth)
	}
	if !strings.Contains(gotBody, `"model":"gemini-embedding-001"`) || !strings.Contains(gotBody, `"input":"hello"`) {
		t.Fatalf("body = %q", gotBody)
	}
	// [3,4] normalized → [0.6, 0.8].
	if math.Abs(float64(vec[0])-0.6) > 1e-6 || math.Abs(float64(vec[1])-0.8) > 1e-6 {
		t.Fatalf("vec = %v, want normalized [0.6 0.8]", vec)
	}
}

func TestHTTPEmbedNoKeyNoHeader(t *testing.T) {
	var gotAuth string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		_, _ = io.WriteString(w, `{"data":[{"embedding":[1]}]}`)
	}))
	defer upstream.Close()

	e := NewHTTP("e", upstream.URL, "m", "", time.Second)
	if _, err := e.Embed(context.Background(), "x"); err != nil {
		t.Fatal(err)
	}
	if gotAuth != "" {
		t.Fatalf("Authorization = %q, want none", gotAuth)
	}
}

func TestHTTPEmbedFailuresSurface(t *testing.T) {
	tests := []struct {
		name string
		body string
		code int
	}{
		{"upstream 500", `boom`, http.StatusInternalServerError},
		{"empty data", `{"data":[]}`, http.StatusOK},
		{"not json", `nope`, http.StatusOK},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tt.code)
				_, _ = io.WriteString(w, tt.body)
			}))
			defer upstream.Close()
			e := NewHTTP("e", upstream.URL, "m", "", time.Second)
			if _, err := e.Embed(context.Background(), "x"); err == nil {
				t.Fatal("want error")
			}
		})
	}
}
