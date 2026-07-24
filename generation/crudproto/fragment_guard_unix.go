//go:build darwin || linux

package crudproto

import (
	"errors"
	"io/fs"
	"os"
	"strings"

	"golang.org/x/sys/unix"
)

func openFragmentDirectory(root, relative string) (*os.File, error) {
	flags := unix.O_RDONLY | unix.O_DIRECTORY | unix.O_NOFOLLOW | unix.O_CLOEXEC
	fd, err := unix.Open(root, flags, 0)
	if err != nil {
		return nil, normalizeFragmentOpenError(err)
	}
	for _, component := range strings.Split(relative, "/") {
		next, openErr := unix.Openat(fd, component, flags, 0)
		unix.Close(fd)
		if openErr != nil {
			return nil, normalizeFragmentOpenError(openErr)
		}
		fd = next
	}
	return os.NewFile(uintptr(fd), relative), nil
}

func openFragmentCandidate(directory *os.File, name string) (*os.File, error) {
	flags := unix.O_RDONLY | unix.O_NONBLOCK | unix.O_NOFOLLOW | unix.O_CLOEXEC
	fd, err := unix.Openat(int(directory.Fd()), name, flags, 0)
	if err != nil {
		return nil, normalizeFragmentOpenError(err)
	}
	return os.NewFile(uintptr(fd), name), nil
}

func normalizeFragmentOpenError(err error) error {
	if errors.Is(err, unix.ENOENT) {
		return fs.ErrNotExist
	}
	return err
}
