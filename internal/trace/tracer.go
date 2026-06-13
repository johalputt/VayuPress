package trace

import "context"

// Tracer creates spans and stores them in its Recorder.
// A zero-value Tracer is valid but drops all spans (no recorder).
type Tracer struct {
	Recorder *Recorder
}

// Global is the application-wide Tracer, initialised in main.
var Global = &Tracer{Recorder: NewRecorder(defaultRingSize)}

// Start creates a new span for operation under any parent span in ctx.
// Call span.End() when the operation completes — use defer for safety.
//
//	ctx, span := trace.Global.Start(ctx, "ArticleService.Create")
//	defer span.End()
func (t *Tracer) Start(ctx context.Context, operation string) (context.Context, *Span) {
	return startSpan(ctx, operation, t.Recorder)
}

// Start is a package-level shortcut that uses the Global tracer.
func Start(ctx context.Context, operation string) (context.Context, *Span) {
	return Global.Start(ctx, operation)
}
