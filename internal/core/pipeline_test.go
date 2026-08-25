package core

import (
	"context"
	"net/http"
	"testing"
)

// tracer returns a Stage that appends name on the way in and name+"'" on the
// way out, so tests can assert composition order in both directions.
func tracer(name string, order *[]string) Stage {
	return func(next Handler) Handler {
		return func(ctx context.Context, req *Request) (*Response, error) {
			*order = append(*order, name)
			resp, err := next(ctx, req)
			*order = append(*order, name+"'")
			return resp, err
		}
	}
}

func TestChainRunsStagesInCompositionOrder(t *testing.T) {
	var order []string
	terminal := func(_ context.Context, _ *Request) (*Response, error) {
		order = append(order, "H")
		return &Response{Status: http.StatusOK}, nil
	}

	h := Chain(tracer("s1", &order), tracer("s2", &order))(terminal)
	if _, err := h(context.Background(), &Request{}); err != nil {
		t.Fatalf("pipeline returned error: %v", err)
	}

	want := []string{"s1", "s2", "H", "s2'", "s1'"}
	if len(order) != len(want) {
		t.Fatalf("order = %v, want %v", order, want)
	}
	for i := range want {
		if order[i] != want[i] {
			t.Fatalf("order = %v, want %v", order, want)
		}
	}
}

func TestStageShortCircuitSkipsDownstream(t *testing.T) {
	shortCircuit := Stage(func(_ Handler) Handler {
		return func(_ context.Context, _ *Request) (*Response, error) {
			return nil, NewError(CodeInvalidRequest, "rejected early")
		}
	})
	terminalCalled := false
	terminal := func(_ context.Context, _ *Request) (*Response, error) {
		terminalCalled = true
		return &Response{Status: http.StatusOK}, nil
	}

	_, err := Chain(shortCircuit)(terminal)(context.Background(), &Request{})
	if err == nil {
		t.Fatal("want error from short-circuiting stage, got nil")
	}
	if terminalCalled {
		t.Fatal("terminal handler ran despite short-circuit")
	}
}

func TestChainWithNoStagesIsIdentity(t *testing.T) {
	terminal := func(_ context.Context, _ *Request) (*Response, error) {
		return &Response{Status: http.StatusOK}, nil
	}
	resp, err := Chain()(terminal)(context.Background(), &Request{})
	if err != nil || resp.Status != http.StatusOK {
		t.Fatalf("identity chain: resp=%v err=%v", resp, err)
	}
}
