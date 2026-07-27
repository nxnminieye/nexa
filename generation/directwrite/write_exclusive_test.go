package directwrite

import (
	"errors"
	"io"
	"io/fs"
	"testing"
)

type fakeExclusiveFile struct {
	chmodErr, writeErr, closeErr error
	n                            int
}

func (f *fakeExclusiveFile) Chmod(fs.FileMode) error   { return f.chmodErr }
func (f *fakeExclusiveFile) Write([]byte) (int, error) { return f.n, f.writeErr }
func (f *fakeExclusiveFile) Close() error              { return f.closeErr }

func TestFinishExclusiveWriteAggregatesChmodAndClose(t *testing.T) {
	chmodErr, closeErr := errors.New("chmod"), errors.New("close")
	err := finishExclusiveWrite(&fakeExclusiveFile{chmodErr: chmodErr, closeErr: closeErr}, []byte("x"), 0o644)
	if !errors.Is(err, chmodErr) || !errors.Is(err, closeErr) {
		t.Fatalf("error = %v", err)
	}
}

func TestFinishExclusiveWriteAggregatesWriteAndClose(t *testing.T) {
	writeErr, closeErr := errors.New("write"), errors.New("close")
	err := finishExclusiveWrite(&fakeExclusiveFile{writeErr: writeErr, closeErr: closeErr, n: 1}, []byte("x"), 0o644)
	if !errors.Is(err, writeErr) || !errors.Is(err, closeErr) {
		t.Fatalf("error = %v", err)
	}
}

func TestFinishExclusiveWriteReturnsCloseOnlyError(t *testing.T) {
	closeErr := errors.New("close")
	err := finishExclusiveWrite(&fakeExclusiveFile{closeErr: closeErr}, nil, 0o644)
	if !errors.Is(err, closeErr) {
		t.Fatalf("error = %v", err)
	}
}

func TestFinishExclusiveWriteDetectsShortWrite(t *testing.T) {
	err := finishExclusiveWrite(&fakeExclusiveFile{n: 0}, []byte("x"), 0o644)
	if !errors.Is(err, io.ErrShortWrite) {
		t.Fatalf("error = %v", err)
	}
}
