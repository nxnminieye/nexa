package provenance

import (
	"unicode"
	"unicode/utf8"

	"golang.org/x/text/unicode/norm"
)

// MaxDomainSourceBytes is the maximum encoded size of a domain source
// coordinate at a typed consumer boundary.
const MaxDomainSourceBytes = 256

// DomainSource is an immutable, repository-relative POSIX coordinate used to
// carry a domain owner's source identity across process boundaries.
type DomainSource struct {
	value string
}

// ParseDomainSource validates value without rewriting it.
func ParseDomainSource(value string) (DomainSource, error) {
	if value == "" {
		return DomainSource{}, newDomainSourceError(domainSourceEmpty)
	}
	if !utf8.ValidString(value) {
		return DomainSource{}, newDomainSourceError(domainSourceInvalidUTF8)
	}
	if len(value) > MaxDomainSourceBytes {
		return DomainSource{}, newDomainSourceError(domainSourceTooLong)
	}
	if !norm.NFC.IsNormalString(value) {
		return DomainSource{}, newDomainSourceError(domainSourceNonNFC)
	}
	for _, character := range value {
		if unicode.Is(unicode.Cc, character) {
			return DomainSource{}, newDomainSourceError(domainSourceControl)
		}
	}
	if value[0] == '/' {
		return DomainSource{}, newDomainSourceError(domainSourceAbsolute)
	}
	if hasPortableVolumePrefix(value) {
		return DomainSource{}, newDomainSourceError(domainSourceVolume)
	}
	for index := 0; index < len(value); index++ {
		if value[index] == '\\' {
			return DomainSource{}, newDomainSourceError(domainSourceBackslash)
		}
	}
	segmentStart := 0
	for index := 0; index <= len(value); index++ {
		if index != len(value) && value[index] != '/' {
			continue
		}
		segment := value[segmentStart:index]
		switch segment {
		case "":
			return DomainSource{}, newDomainSourceError(domainSourceEmptySegment)
		case ".":
			return DomainSource{}, newDomainSourceError(domainSourceDotSegment)
		case "..":
			return DomainSource{}, newDomainSourceError(domainSourceParentSegment)
		}
		segmentStart = index + 1
	}
	return DomainSource{value: value}, nil
}

// String returns the byte-exact validated coordinate. The zero value returns
// an empty string and is invalid at typed consumer boundaries.
func (s DomainSource) String() string {
	return s.value
}

type domainSourceFailure uint8

const (
	domainSourceEmpty domainSourceFailure = iota + 1
	domainSourceInvalidUTF8
	domainSourceTooLong
	domainSourceNonNFC
	domainSourceControl
	domainSourceAbsolute
	domainSourceVolume
	domainSourceBackslash
	domainSourceEmptySegment
	domainSourceDotSegment
	domainSourceParentSegment
)

type domainSourceError struct {
	failure domainSourceFailure
}

func newDomainSourceError(failure domainSourceFailure) *domainSourceError {
	return &domainSourceError{failure: failure}
}

func (e *domainSourceError) Error() string {
	if e == nil {
		return ""
	}
	return "invalid domain source"
}
