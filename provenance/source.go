package provenance

import (
	"encoding/json"
	"net/url"
	"strings"
	"unicode/utf8"
)

const repositoryScheme = "repo:"

type SourceRef struct {
	value string
}

func ParseSourceRef(value string) (SourceRef, error) {
	if !strings.HasPrefix(value, repositoryScheme) {
		return SourceRef{}, invalid("source reference", "only the repo scheme is supported")
	}
	encodedPath, encodedFragment, found := strings.Cut(strings.TrimPrefix(value, repositoryScheme), "#")
	if found && encodedFragment == "" {
		return SourceRef{}, invalid("source reference", "explicit empty fragment is not allowed")
	}
	path, err := url.PathUnescape(encodedPath)
	if err != nil {
		return SourceRef{}, invalid("source reference", "path contains invalid percent encoding")
	}
	fragment := ""
	if found {
		fragment, err = url.PathUnescape(encodedFragment)
		if err != nil {
			return SourceRef{}, invalid("source reference", "fragment contains invalid percent encoding")
		}
	}
	canonical, err := RepositoryRef(path, fragment)
	if err != nil {
		return SourceRef{}, err
	}
	if canonical.value != value {
		return SourceRef{}, invalid("source reference", "reference is not canonically encoded")
	}
	return canonical, nil
}

func RepositoryRef(path, fragment string) (SourceRef, error) {
	if err := validateRepositoryPath(path); err != nil {
		return SourceRef{}, err
	}
	if err := validateRepositoryFragment(fragment); err != nil {
		return SourceRef{}, err
	}

	components := strings.Split(path, "/")
	for index := range components {
		components[index] = percentEncode(components[index])
	}
	value := repositoryScheme + strings.Join(components, "/")
	if fragment != "" {
		value += "#" + percentEncode(fragment)
	}
	return SourceRef{
		value: value,
	}, nil
}

func (r SourceRef) String() string {
	return r.value
}

func (r SourceRef) Path() string {
	if r.value == "" {
		return ""
	}
	encodedPath, _, _ := strings.Cut(strings.TrimPrefix(r.value, repositoryScheme), "#")
	path, _ := url.PathUnescape(encodedPath)
	return path
}

func (r SourceRef) Fragment() string {
	if r.value == "" {
		return ""
	}
	_, encodedFragment, _ := strings.Cut(strings.TrimPrefix(r.value, repositoryScheme), "#")
	fragment, _ := url.PathUnescape(encodedFragment)
	return fragment
}

func (r SourceRef) MarshalJSON() ([]byte, error) {
	if r.value == "" {
		return nil, invalid("source reference", "zero value cannot cross a document boundary")
	}
	return json.Marshal(r.value)
}

type Source struct {
	Ref    SourceRef `json:"ref"`
	Digest Digest    `json:"digest"`
}

func (s Source) MarshalJSON() ([]byte, error) {
	if s.Ref.value == "" {
		return nil, invalid("source", "reference is required")
	}
	if s.Digest.value == "" {
		return nil, invalid("source", "digest is required")
	}
	type documentSource Source
	return json.Marshal(documentSource(s))
}

func validateRepositoryPath(path string) error {
	if path == "" {
		return invalid("repository path", "path is required")
	}
	if !utf8.ValidString(path) {
		return invalid("repository path", "path must be valid UTF-8")
	}
	if strings.HasPrefix(path, "/") {
		return invalid("repository path", "absolute paths are not allowed")
	}
	if hasPortableVolumePrefix(path) {
		return invalid("repository path", "volume paths are not allowed")
	}
	if strings.ContainsAny(path, "\\\x00?") {
		return invalid("repository path", "backslash, NUL, and query are not allowed")
	}
	for _, component := range strings.Split(path, "/") {
		if component == "" || component == "." || component == ".." {
			return invalid("repository path", "empty and dot components are not allowed")
		}
	}
	return nil
}

func hasPortableVolumePrefix(path string) bool {
	firstComponent, _, _ := strings.Cut(path, "/")
	return len(firstComponent) >= 2 && isASCIILetter(firstComponent[0]) && firstComponent[1] == ':'
}

func isASCIILetter(character byte) bool {
	return character >= 'a' && character <= 'z' || character >= 'A' && character <= 'Z'
}

func validateRepositoryFragment(fragment string) error {
	if !utf8.ValidString(fragment) {
		return invalid("repository fragment", "fragment must be valid UTF-8")
	}
	if strings.ContainsAny(fragment, "\x00?") {
		return invalid("repository fragment", "NUL and query are not allowed")
	}
	return nil
}

func percentEncode(value string) string {
	const hexadecimal = "0123456789ABCDEF"
	var encoded strings.Builder
	for index := 0; index < len(value); index++ {
		character := value[index]
		if isUnreserved(character) {
			encoded.WriteByte(character)
			continue
		}
		encoded.WriteByte('%')
		encoded.WriteByte(hexadecimal[character>>4])
		encoded.WriteByte(hexadecimal[character&0x0f])
	}
	return encoded.String()
}

func isUnreserved(character byte) bool {
	return character >= 'a' && character <= 'z' ||
		character >= 'A' && character <= 'Z' ||
		character >= '0' && character <= '9' ||
		character == '-' || character == '.' || character == '_' || character == '~'
}
