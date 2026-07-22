package engine

import "context"

type TextMergeInput struct {
	Old   []byte
	Local []byte
	New   []byte
}

type TextMergeResult struct {
	content []byte
	clean   bool
}

func NewTextMergeResult(content []byte, clean bool) TextMergeResult {
	return TextMergeResult{content: append([]byte(nil), content...), clean: clean}
}

func (r TextMergeResult) Bytes() []byte { return append([]byte(nil), r.content...) }
func (r TextMergeResult) Clean() bool   { return r.clean }

type MergeDriver interface {
	Merge(context.Context, TextMergeInput) (TextMergeResult, error)
}
