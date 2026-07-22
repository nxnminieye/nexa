package contract

import (
	"io/fs"
	"strings"
	"unicode"
	"unicode/utf8"

	"golang.org/x/text/cases"
	"golang.org/x/text/unicode/norm"
)

type PathReason uint8

const (
	PathInvalid PathReason = iota + 1
	PathNotNFC
	PathReserved
)

type PathIssue struct {
	Reason PathReason
}

func (r PathReason) MachineReason() (string, bool) {
	switch r {
	case PathInvalid:
		return "path_invalid", true
	case PathNotNFC:
		return "path_not_nfc", true
	case PathReserved:
		return "path_reserved", true
	default:
		return "", false
	}
}

func ValidatePortablePath(value string) *PathIssue {
	if !utf8.ValidString(value) || value == "" {
		return &PathIssue{Reason: PathInvalid}
	}
	if !norm.NFC.IsNormalString(value) {
		return &PathIssue{Reason: PathNotNFC}
	}
	if value == "." || !fs.ValidPath(value) || PortableVolumePath(value) || strings.ContainsAny(value, "\\\x00") {
		return &PathIssue{Reason: PathInvalid}
	}
	for _, character := range value {
		if unicode.Is(unicode.Cc, character) || unicode.Is(unicode.Cf, character) {
			return &PathIssue{Reason: PathInvalid}
		}
	}
	if ReservedControlPath(value) {
		return &PathIssue{Reason: PathReserved}
	}
	return nil
}

func ReservedControlPath(value string) bool {
	first, remainder, _ := strings.Cut(value, "/")
	if strings.EqualFold(first, ".git") {
		return true
	}
	second, _, _ := strings.Cut(remainder, "/")
	return strings.EqualFold(first, ".nexa") && strings.EqualFold(second, "source")
}

func FoldPortablePath(value string) string { return cases.Fold().String(value) }

func PortableVolumePath(value string) bool {
	component, _, _ := strings.Cut(value, "/")
	return len(component) >= 2 && ((component[0] >= 'a' && component[0] <= 'z') || (component[0] >= 'A' && component[0] <= 'Z')) && component[1] == ':'
}
