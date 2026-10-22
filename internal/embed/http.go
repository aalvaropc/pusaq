package embed

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// HTTP embeds through any OpenAI-compatible POST /embeddings endpoint —
// the same seam the chat providers use, with the same bearer-key and
// bounded-client discipline.
type HTTP struct {
	name    string
	baseURL string
	model   string
	apiKey  string
	client  *http.Client
}

// NewHTTP builds an adapter for the API at baseURL, version included and
// without a trailing slash, embedding with model at baseURL/embeddings;
// every call is bounded by timeout. An empty apiKey sends no credentials.
func NewHTTP(name, baseURL, model, apiKey string, timeout time.Duration) *HTTP {
	return &HTTP{
		name:    name,
		baseURL: baseURL,
		model:   model,
		apiKey:  apiKey,
		client:  &http.Client{Timeout: timeout, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }},
	}
}

// Name implements Embedder.
func (h *HTTP) Name() string { return h.name }

// Embed implements Embedder.
func (h *HTTP) Embed(ctx context.Context, text string) ([]float32, error) {
	payload, err := json.Marshal(map[string]any{
		"model": h.model,
		"input": text,
	})
	if err != nil {
		return nil, fmt.Errorf("marshal embeddings request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		h.baseURL+"/embeddings", bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("build embeddings request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if h.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+h.apiKey)
	}

	resp, err := h.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("embeddings call: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return nil, fmt.Errorf("read embeddings response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("embeddings endpoint returned status %d", resp.StatusCode)
	}

	var parsed struct {
		Data []struct {
			Embedding []float32 `json:"embedding"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, fmt.Errorf("invalid embeddings response: %w", err)
	}
	if len(parsed.Data) == 0 || len(parsed.Data[0].Embedding) == 0 {
		return nil, fmt.Errorf("embeddings response carried no vector")
	}
	return Normalize(parsed.Data[0].Embedding), nil
}
