package annotation

import "context"

type progressKey struct{}

// WithProgress attaches a job-owned progress sink without coupling services to UI.
func WithProgress(ctx context.Context, report func(int, string)) context.Context {
	return context.WithValue(ctx, progressKey{}, report)
}

func ReportProgress(ctx context.Context, percent int, message string) {
	if report, ok := ctx.Value(progressKey{}).(func(int, string)); ok && ctx.Err() == nil {
		report(percent, message)
	}
}
