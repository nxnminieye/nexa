package release

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"path"
	"regexp"
	"sort"
	"strings"

	"golang.org/x/mod/module"
	"golang.org/x/mod/semver"
)

const (
	ManifestSchemaVersion = "nexa.dev/release-manifest/v1"
	maxJSONBytes          = 4 << 20
)

var (
	commitPattern = regexp.MustCompile(`^[0-9a-f]{40}([0-9a-f]{24})?$`)
	digestPattern = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
)

type Candidate struct {
	Repository string `json:"repository"`
	Module     string `json:"module"`
	Version    string `json:"version"`
	Tag        string `json:"tag"`
	Commit     string `json:"commit"`
}

type Artifact struct {
	Path      string `json:"path"`
	Size      int64  `json:"size"`
	SHA256    string `json:"sha256"`
	MediaType string `json:"mediaType"`
}

type Dependency struct {
	Module            string `json:"module"`
	Version           string `json:"version"`
	LicenseExpression string `json:"licenseExpression"`
	SHA256            string `json:"sha256"`
}

type Manifest struct {
	SchemaVersion string       `json:"schemaVersion"`
	Candidate     Candidate    `json:"candidate"`
	Artifacts     []Artifact   `json:"artifacts"`
	Dependencies  []Dependency `json:"dependencies"`
}

func ParseManifest(reader io.Reader) (Manifest, error) {
	var manifest Manifest
	if err := decodeJSON(reader, maxJSONBytes, &manifest); err != nil {
		return Manifest{}, fmt.Errorf("decode release manifest: %w", err)
	}
	if err := validateManifest(manifest); err != nil {
		return Manifest{}, err
	}
	return manifest, nil
}

func MarshalManifest(manifest Manifest) ([]byte, error) {
	if err := validateManifest(manifest); err != nil {
		return nil, err
	}
	return json.Marshal(manifest)
}

func validateManifest(manifest Manifest) error {
	if manifest.SchemaVersion != ManifestSchemaVersion {
		return fmt.Errorf("release manifest schema version is invalid")
	}
	if err := validateCandidate(manifest.Candidate); err != nil {
		return err
	}
	if manifest.Artifacts == nil || manifest.Dependencies == nil {
		return fmt.Errorf("release manifest collections are required")
	}
	previous := ""
	for _, artifact := range manifest.Artifacts {
		if !safeArchivePath(artifact.Path) || artifact.Path <= previous || artifact.Size < 0 ||
			!digestPattern.MatchString(artifact.SHA256) || strings.TrimSpace(artifact.MediaType) == "" {
			return fmt.Errorf("release artifact is invalid")
		}
		previous = artifact.Path
	}
	previous = ""
	for _, dependency := range manifest.Dependencies {
		key := dependency.Module + "@" + dependency.Version
		if err := validateDependency(dependency); err != nil || key <= previous {
			return fmt.Errorf("release dependency is invalid")
		}
		previous = key
	}
	return nil
}

func validateCandidate(candidate Candidate) error {
	if !validRepositoryURL(candidate.Repository) || !validModuleVersion(candidate.Module, candidate.Version) ||
		candidate.Tag != candidate.Version || !commitPattern.MatchString(candidate.Commit) {
		return fmt.Errorf("release candidate is invalid")
	}
	return nil
}

func validateDependency(dependency Dependency) error {
	if !validModuleVersion(dependency.Module, dependency.Version) || !validSPDXExpression(dependency.LicenseExpression) ||
		!digestPattern.MatchString(dependency.SHA256) {
		return fmt.Errorf("dependency is invalid")
	}
	return nil
}

func validRepositoryURL(value string) bool {
	parsed, err := url.ParseRequestURI(value)
	return err == nil && parsed.Scheme == "https" && parsed.Host != "" && parsed.User == nil && parsed.Opaque == "" &&
		parsed.RawQuery == "" && parsed.Fragment == "" && parsed.RawPath == "" && parsed.Path != "" && parsed.Path != "/" &&
		path.Clean(parsed.Path) == parsed.Path && !strings.Contains(parsed.Path, "//")
}

func validModuleVersion(modulePath, version string) bool {
	if module.CheckPath(modulePath) != nil || !semver.IsValid(version) {
		return false
	}
	_, pathMajor, ok := module.SplitPathVersion(modulePath)
	return ok && module.CheckPathMajor(version, pathMajor) == nil
}

func sortedDependencies(input []Dependency) ([]Dependency, error) {
	result := append([]Dependency(nil), input...)
	for _, dependency := range result {
		if err := validateDependency(dependency); err != nil {
			return nil, err
		}
	}
	sort.Slice(result, func(i, j int) bool {
		left, right := result[i].Module+"@"+result[i].Version, result[j].Module+"@"+result[j].Version
		return left < right
	})
	for index := 1; index < len(result); index++ {
		if result[index-1].Module == result[index].Module && result[index-1].Version == result[index].Version {
			return nil, fmt.Errorf("dependency is duplicated")
		}
	}
	return result, nil
}

func decodeJSON(reader io.Reader, limit int64, target any) error {
	if reader == nil {
		return fmt.Errorf("JSON reader is required")
	}
	data, err := io.ReadAll(io.LimitReader(reader, limit+1))
	if err != nil {
		return err
	}
	if int64(len(data)) > limit {
		return fmt.Errorf("JSON document exceeds %d bytes", limit)
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return fmt.Errorf("JSON document contains trailing input")
	}
	return nil
}

func safeArchivePath(value string) bool {
	return value != "" && value != "." && !path.IsAbs(value) && path.Clean(value) == value && !strings.HasPrefix(value, "../")
}
