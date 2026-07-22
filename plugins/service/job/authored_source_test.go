package job_test

import (
	"embed"
	"io/fs"
	"testing"

	"github.com/nxnminieye/nexa/internal/bundletest"
)

//go:embed _bundle/backend/job/jobapp
var authoredJob embed.FS

func TestAuthoredJobSource(t *testing.T) {
	t.Parallel()

	source, err := fs.Sub(authoredJob, "_bundle/backend/job")
	if err != nil {
		t.Fatal(err)
	}
	err = bundletest.Run(t.Context(), bundletest.Module{
		Path:   "example.com/job-source",
		Source: source,
		Requirements: map[string]string{
			"github.com/robfig/cron/v3": "v3.0.1",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
}
