package repobot

import "context"

type Progress struct {
	Phase, Task string
}
type progressKey struct{}

func WithProgress(ctx context.Context, observe func(Progress)) context.Context {
	return context.WithValue(ctx, progressKey{}, observe)
}
func observe(ctx context.Context) func(Progress) {
	if fn, _ := ctx.Value(progressKey{}).(func(Progress)); fn != nil {
		return fn
	}
	return func(Progress) {}
}
