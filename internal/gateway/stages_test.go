package gateway

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"testing"

	"github.com/aalvaropc/pusaq/internal/core"
)

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// runPipeline composes the production stage order around terminal and runs
// one request through it, returning the emitted events.
func runPipeline(t *testing.T, terminal core.Handler) ([]core.CostEvent, *core.Response, error) {
	t.Helper()
	var events []core.CostEvent
	emit := func(e core.CostEvent) { events = append(events, e) }
	h := core.Chain(Accounting(emit, discardLogger()), RequestID())(terminal)
	resp, err := h(context.Background(), &core.Request{Op: "test"})
	return events, resp, err
}

func TestExactlyOneCostEventOnSuccess(t *testing.T) {
	events, resp, err := runPipeline(t, func(_ context.Context, req *core.Request) (*core.Response, error) {
		return &core.Response{Status: http.StatusOK}, nil
	})
	if err != nil {
		t.Fatalf("pipeline error: %v", err)
	}
	if resp == nil || resp.Status != http.StatusOK {
		t.Fatalf("resp = %+v", resp)
	}
	if len(events) != 1 {
		t.Fatalf("emitted %d cost events, want exactly 1", len(events))
	}
	e := events[0]
	if e.Outcome != core.OutcomeSuccess || e.Op != "test" || e.RequestID == "" {
		t.Fatalf("event = %+v", e)
	}
}

func TestExactlyOneCostEventOnHandlerError(t *testing.T) {
	events, _, err := runPipeline(t, func(_ context.Context, _ *core.Request) (*core.Response, error) {
		return nil, core.NewError(core.CodeInvalidRequest, "bad payload")
	})
	if err == nil {
		t.Fatal("want error, got nil")
	}
	if len(events) != 1 {
		t.Fatalf("emitted %d cost events, want exactly 1", len(events))
	}
	e := events[0]
	if e.Outcome != core.OutcomeError || e.ErrorCode != core.CodeInvalidRequest {
		t.Fatalf("event = %+v", e)
	}
}

func TestExactlyOneCostEventOnPanic(t *testing.T) {
	events, resp, err := runPipeline(t, func(_ context.Context, _ *core.Request) (*core.Response, error) {
		panic("handler exploded")
	})
	if resp != nil {
		t.Fatalf("resp = %+v, want nil after recovered panic", resp)
	}
	var norm *core.Error
	if !errors.As(err, &norm) || norm.Code != core.CodeInternal {
		t.Fatalf("err = %v, want normalized internal error", err)
	}
	if len(events) != 1 {
		t.Fatalf("emitted %d cost events, want exactly 1", len(events))
	}
	e := events[0]
	if e.Outcome != core.OutcomePanic || e.ErrorCode != core.CodeInternal || e.RequestID == "" {
		t.Fatalf("event = %+v", e)
	}
}

func TestRequestIDAssignedAndStable(t *testing.T) {
	var seen string
	events, _, err := runPipeline(t, func(_ context.Context, req *core.Request) (*core.Response, error) {
		seen = req.ID
		return &core.Response{Status: http.StatusOK}, nil
	})
	if err != nil {
		t.Fatalf("pipeline error: %v", err)
	}
	if seen == "" {
		t.Fatal("terminal handler saw an empty request ID")
	}
	if events[0].RequestID != seen {
		t.Fatalf("cost event ID %q != handler-observed ID %q", events[0].RequestID, seen)
	}
}

func TestRequestIDsAreUnique(t *testing.T) {
	ids := make(map[string]bool)
	for range 100 {
		id := newRequestID()
		if ids[id] {
			t.Fatalf("duplicate request ID %q", id)
		}
		ids[id] = true
	}
}
