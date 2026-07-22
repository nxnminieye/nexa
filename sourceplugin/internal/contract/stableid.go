package contract

import (
	"regexp"
	"unicode/utf8"
)

const MaxStableIDBytes = 128

var stableIDPattern = regexp.MustCompile(`^[a-z][a-z0-9]*(?:[.-][a-z0-9]+)*$`)

func ValidStableID(value string) bool {
	return utf8.ValidString(value) && len(value) >= 1 && len(value) <= MaxStableIDBytes && stableIDPattern.MatchString(value)
}
