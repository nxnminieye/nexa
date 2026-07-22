package sourceplugin

import (
	"io/fs"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/nxnminieye/nexa/sourceplugin/internal/contract"
	"golang.org/x/text/unicode/norm"
)

const MaxSourceLabelBytes = 1024

func validateSourceLabel(source string) bool {
	if len(source) == 0 || len(source) > MaxSourceLabelBytes || !utf8.ValidString(source) || !norm.NFC.IsNormalString(source) {
		return false
	}
	if source == "." || !fs.ValidPath(source) || portableVolumePath(source) || strings.ContainsAny(source, "\\\x00") {
		return false
	}
	for _, character := range source {
		if unicode.Is(unicode.Cc, character) || unicode.Is(unicode.Cf, character) {
			return false
		}
	}
	return !reservedControlPath(source)
}

func projectPathIssue(issue *contract.PathIssue, pointer string) (string, *Error) {
	if issue == nil {
		return "", nil
	}
	reason, ok := issue.Reason.MachineReason()
	if !ok {
		return "", newContractInternal("path_issue_unmapped", pointer)
	}
	return reason, nil
}

func validatePortablePath(value, pointer string) (string, *Error) {
	return projectPathIssue(contract.ValidatePortablePath(value), pointer)
}

func reservedControlPath(value string) bool {
	return contract.ReservedControlPath(value)
}

func foldPortablePath(value string) string { return contract.FoldPortablePath(value) }

func portableVolumePath(value string) bool {
	return contract.PortableVolumePath(value)
}
