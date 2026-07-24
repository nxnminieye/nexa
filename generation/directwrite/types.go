package directwrite

import "context"

// OutputMode defines how a generated output scope is maintained.
type OutputMode string

const (
	OutputModeReplaceTree OutputMode = "replace-tree"
	OutputModeFileSet     OutputMode = "file-set"
)

// OutputScope is a repository-relative generated-source boundary.
type OutputScope struct {
	Path string     `json:"path"`
	Mode OutputMode `json:"mode"`
}

// OutputFile is one desired generated file.
type OutputFile struct {
	Path    string
	Content []byte
}

// MutationSet is one ephemeral, in-process direct-write request.
type MutationSet struct {
	Scopes  []OutputScope
	Writes  []OutputFile
	Deletes []string
}

// WriteReport records host mutations completed before Write returned.
type WriteReport struct {
	CompletedWrites  []string `json:"completedWrites"`
	CompletedDeletes []string `json:"completedDeletes"`
}

// Write prevalidates the complete request and then applies it directly.
func Write(ctx context.Context, repositoryRoot string, mutations MutationSet) (WriteReport, error) {
	return write(ctx, repositoryRoot, mutations, osFileSystem{})
}
