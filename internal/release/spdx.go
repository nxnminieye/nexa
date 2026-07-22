package release

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/github/go-spdx/v2/spdxexp"
	"golang.org/x/mod/semver"
)

const (
	SPDXVersion              = "SPDX-2.3"
	RelicensingRequirementID = "LEGAL-RELICENSE-001"
)

var (
	spdxIDPartPattern    = regexp.MustCompile(`[^A-Za-z0-9.-]+`)
	packageSPDXIDPattern = regexp.MustCompile(`^SPDXRef-[A-Za-z0-9.-]+$`)
)

type SPDXCreationInfo struct {
	Created  string   `json:"created"`
	Creators []string `json:"creators"`
}

type SPDXChecksum struct {
	Algorithm     string `json:"algorithm"`
	ChecksumValue string `json:"checksumValue"`
}

type SPDXPackage struct {
	Name             string         `json:"name"`
	SPDXID           string         `json:"SPDXID"`
	VersionInfo      string         `json:"versionInfo"`
	DownloadLocation string         `json:"downloadLocation"`
	FilesAnalyzed    bool           `json:"filesAnalyzed"`
	LicenseConcluded string         `json:"licenseConcluded"`
	LicenseDeclared  string         `json:"licenseDeclared"`
	CopyrightText    string         `json:"copyrightText"`
	Checksums        []SPDXChecksum `json:"checksums"`
}

type SPDXRelationship struct {
	SPDXElementID      string `json:"spdxElementId"`
	RelationshipType   string `json:"relationshipType"`
	RelatedSPDXElement string `json:"relatedSpdxElement"`
}

type SPDXDocument struct {
	SPDXVersion       string             `json:"spdxVersion"`
	DataLicense       string             `json:"dataLicense"`
	SPDXID            string             `json:"SPDXID"`
	Name              string             `json:"name"`
	DocumentNamespace string             `json:"documentNamespace"`
	CreationInfo      SPDXCreationInfo   `json:"creationInfo"`
	Packages          []SPDXPackage      `json:"packages"`
	Relationships     []SPDXRelationship `json:"relationships"`
}

type LegalInventory struct {
	SPDXSHA256           string       `json:"spdxSha256"`
	NoticeSHA256         string       `json:"noticeSha256"`
	Dependencies         []Dependency `json:"dependencies"`
	ExternalRequirements []string     `json:"externalRequirements"`
}

func BuildSPDX(name, namespace string, created time.Time, dependencies []Dependency) (SPDXDocument, error) {
	ordered, err := sortedDependencies(dependencies)
	if err != nil {
		return SPDXDocument{}, err
	}
	parsedURL, err := url.Parse(namespace)
	if err != nil || parsedURL.Scheme != "https" || parsedURL.Host == "" || strings.TrimSpace(name) == "" || created.IsZero() {
		return SPDXDocument{}, fmt.Errorf("SPDX document identity is invalid")
	}
	document := SPDXDocument{
		SPDXVersion: SPDXVersion, DataLicense: "CC0-1.0", SPDXID: "SPDXRef-DOCUMENT", Name: name,
		DocumentNamespace: namespace,
		CreationInfo:      SPDXCreationInfo{Created: created.UTC().Format(time.RFC3339), Creators: []string{"Tool: nexa-release"}},
		Packages:          make([]SPDXPackage, len(ordered)),
		Relationships:     make([]SPDXRelationship, len(ordered)),
	}
	for index, dependency := range ordered {
		coordinate := dependency.Module + "@" + dependency.Version
		coordinateDigest := sha256.Sum256([]byte(coordinate))
		document.Packages[index] = SPDXPackage{
			Name:        dependency.Module,
			SPDXID:      "SPDXRef-Package-" + spdxIDPartPattern.ReplaceAllString(dependency.Module+"-"+dependency.Version, "-") + "-" + hex.EncodeToString(coordinateDigest[:6]),
			VersionInfo: dependency.Version, DownloadLocation: "NOASSERTION", FilesAnalyzed: false,
			LicenseConcluded: "NOASSERTION", LicenseDeclared: dependency.LicenseExpression, CopyrightText: "NOASSERTION",
			Checksums: []SPDXChecksum{{Algorithm: "SHA256", ChecksumValue: strings.TrimPrefix(dependency.SHA256, "sha256:")}},
		}
		document.Relationships[index] = SPDXRelationship{
			SPDXElementID:      document.SPDXID,
			RelationshipType:   "DESCRIBES",
			RelatedSPDXElement: document.Packages[index].SPDXID,
		}
	}
	if err := validateSPDX(document); err != nil {
		return SPDXDocument{}, err
	}
	return document, nil
}

func ParseSPDX(reader io.Reader) (SPDXDocument, error) {
	var document SPDXDocument
	if err := decodeJSON(reader, maxJSONBytes, &document); err != nil {
		return SPDXDocument{}, fmt.Errorf("decode SPDX document: %w", err)
	}
	if err := validateSPDX(document); err != nil {
		return SPDXDocument{}, err
	}
	return document, nil
}

func MarshalSPDX(document SPDXDocument) ([]byte, error) {
	if err := validateSPDX(document); err != nil {
		return nil, err
	}
	return json.Marshal(document)
}

func validateSPDX(document SPDXDocument) error {
	if document.SPDXVersion != SPDXVersion || document.DataLicense != "CC0-1.0" || document.SPDXID != "SPDXRef-DOCUMENT" ||
		strings.TrimSpace(document.Name) == "" || document.Packages == nil || len(document.CreationInfo.Creators) == 0 {
		return fmt.Errorf("SPDX document is invalid")
	}
	parsedURL, err := url.Parse(document.DocumentNamespace)
	if err != nil || parsedURL.Scheme != "https" || parsedURL.Host == "" {
		return fmt.Errorf("SPDX namespace is invalid")
	}
	if _, err := time.Parse(time.RFC3339, document.CreationInfo.Created); err != nil {
		return fmt.Errorf("SPDX creation time is invalid")
	}
	previous := ""
	identifiers := make(map[string]struct{}, len(document.Packages))
	for _, pkg := range document.Packages {
		coordinate := pkg.Name + "@" + pkg.VersionInfo
		if coordinate <= previous || !packageSPDXIDPattern.MatchString(pkg.SPDXID) || pkg.SPDXID == "SPDXRef-DOCUMENT" ||
			!semver.IsValid(pkg.VersionInfo) || pkg.DownloadLocation == "" || pkg.FilesAnalyzed ||
			(pkg.LicenseConcluded != "NOASSERTION" && !validSPDXExpression(pkg.LicenseConcluded)) || !validSPDXExpression(pkg.LicenseDeclared) ||
			pkg.CopyrightText == "" || len(pkg.Checksums) != 1 ||
			pkg.Checksums[0].Algorithm != "SHA256" || len(pkg.Checksums[0].ChecksumValue) != 64 {
			return fmt.Errorf("SPDX package is invalid")
		}
		if _, duplicate := identifiers[pkg.SPDXID]; duplicate {
			return fmt.Errorf("SPDX package identifier is duplicated")
		}
		identifiers[pkg.SPDXID] = struct{}{}
		if _, err := hex.DecodeString(pkg.Checksums[0].ChecksumValue); err != nil {
			return fmt.Errorf("SPDX package checksum is invalid")
		}
		previous = coordinate
	}
	if len(document.Relationships) != len(document.Packages) {
		return fmt.Errorf("SPDX relationships are invalid")
	}
	for index, relationship := range document.Relationships {
		if relationship.SPDXElementID != document.SPDXID || relationship.RelationshipType != "DESCRIBES" ||
			relationship.RelatedSPDXElement != document.Packages[index].SPDXID {
			return fmt.Errorf("SPDX relationship is invalid")
		}
	}
	return nil
}

func BuildLegalInventory(spdxBytes, noticeBytes []byte, dependencies []Dependency) (LegalInventory, error) {
	document, err := ParseSPDX(strings.NewReader(string(spdxBytes)))
	if err != nil {
		return LegalInventory{}, err
	}
	ordered, err := sortedDependencies(dependencies)
	if err != nil {
		return LegalInventory{}, err
	}
	if !spdxMatchesDependencies(document, ordered) {
		return LegalInventory{}, fmt.Errorf("SPDX packages do not match dependency inventory")
	}
	spdxDigest, noticeDigest := sha256.Sum256(spdxBytes), sha256.Sum256(noticeBytes)
	return LegalInventory{
		SPDXSHA256: "sha256:" + hex.EncodeToString(spdxDigest[:]), NoticeSHA256: "sha256:" + hex.EncodeToString(noticeDigest[:]),
		Dependencies: ordered, ExternalRequirements: []string{RelicensingRequirementID},
	}, nil
}

func spdxMatchesDependencies(document SPDXDocument, dependencies []Dependency) bool {
	if len(document.Packages) != len(dependencies) {
		return false
	}
	for index, dependency := range dependencies {
		pkg := document.Packages[index]
		if pkg.Name != dependency.Module || pkg.VersionInfo != dependency.Version || pkg.LicenseDeclared != dependency.LicenseExpression ||
			len(pkg.Checksums) != 1 || pkg.Checksums[0].Algorithm != "SHA256" ||
			pkg.Checksums[0].ChecksumValue != strings.TrimPrefix(dependency.SHA256, "sha256:") {
			return false
		}
	}
	return true
}

func validSPDXExpression(expression string) bool {
	if expression == "" || strings.TrimSpace(expression) != expression {
		return false
	}
	licenses, err := spdxexp.ExtractLicenses(expression)
	if err != nil || len(licenses) == 0 {
		return false
	}
	valid, invalid := spdxexp.ValidateLicensesWithOptions(licenses, spdxexp.ValidateLicensesOptions{
		FailDeprecatedLicenses: true,
		FailAllLicenseRefs:     true,
		FailAllDocumentRefs:    true,
	})
	return valid && len(invalid) == 0
}
