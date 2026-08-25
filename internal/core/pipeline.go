package core

import "context"

// Handler is the terminal function of a pipeline: it turns a Request into a
// Response or an error. Stages wrap Handlers, so the whole pipeline is itself
// a Handler.
type Handler func(ctx context.Context, req *Request) (*Response, error)

// Stage decorates a Handler with a cross-cutting concern (identification,
// accounting, and — in later slices — limiting, policy, caching, routing).
// A stage may short-circuit by returning without calling next.
type Stage func(next Handler) Handler

// Chain composes stages into one. Chain(s1, s2)(h) runs s1 outermost:
// s1 observes the request first and the response last.
func Chain(stages ...Stage) Stage {
	return func(next Handler) Handler {
		for i := len(stages) - 1; i >= 0; i-- {
			next = stages[i](next)
		}
		return next
	}
}
