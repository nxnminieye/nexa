//go:build !darwin && !linux

package crudproto

import "os"

func openFragmentDirectory(string, string) (*os.File, error) {
	return nil, errFragmentGuardPlatformUnsupported
}

func openFragmentCandidate(*os.File, string) (*os.File, error) {
	return nil, errFragmentGuardPlatformUnsupported
}
