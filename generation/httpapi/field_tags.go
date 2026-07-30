package httpapi

import (
	"errors"
	"fmt"
	"strings"
	"unicode"

	"github.com/nxnminieye/nexa/generation/httpconvention"
	"github.com/nxnminieye/nexa/provenance"
	"github.com/zeromicro/go-zero/tools/goctl/api/spec"
)

const (
	originRefTag    = "nexaOriginRef"
	originDigestTag = "nexaOriginDigest"
)

func parseFieldTags(raw string) (provenance.Source, bool, error) {
	if raw == "" {
		return provenance.Source{}, false, nil
	}
	tags, err := spec.Parse(raw)
	if err != nil {
		return provenance.Source{}, false, err
	}
	seen := map[string]bool{}
	var refValue, digestValue string
	for _, tag := range tags.Tags() {
		if seen[tag.Key] {
			return provenance.Source{}, false, fmt.Errorf("duplicate struct tag %q", tag.Key)
		}
		seen[tag.Key] = true
		switch tag.Key {
		case "path", "form", "json":
			continue
		case "header":
			return provenance.Source{}, false, fmt.Errorf("authored transport tag %q is forbidden", tag.Key)
		case originRefTag:
			refValue = tag.Name
		case originDigestTag:
			digestValue = tag.Name
		default:
			if strings.HasPrefix(tag.Key, "nexa") {
				return provenance.Source{}, false, fmt.Errorf("unknown Nexa struct tag %q", tag.Key)
			}
			return provenance.Source{}, false, fmt.Errorf("authored struct tag %q is forbidden", tag.Key)
		}
	}
	if (refValue == "") != (digestValue == "") {
		return provenance.Source{}, false, fmt.Errorf("field origin tags must be declared together")
	}
	if refValue == "" {
		return provenance.Source{}, false, nil
	}
	ref, err := provenance.ParseSourceRef(refValue)
	if err != nil {
		return provenance.Source{}, false, err
	}
	digest, err := provenance.ParseDigest(digestValue)
	if err != nil {
		return provenance.Source{}, false, err
	}
	return provenance.Source{Ref: ref, Digest: digest}, true, nil
}

// externalFieldName treats a supported .api tag as the single external field
// identity. It may repeat the source spelling or its deterministic lowerCamel
// form, but cannot introduce an arbitrary wire alias.
func externalFieldName(sourceName, raw string) (string, httpconvention.Location, bool, error) {
	canonical, err := httpconvention.CanonicalName(sourceName)
	if err != nil {
		return "", "", false, err
	}
	if raw == "" {
		if err := httpconvention.ValidateFieldName(sourceName); err == nil {
			return sourceName, "", false, nil
		}
		return canonical, "", false, nil
	}
	tags, err := spec.Parse(raw)
	if err != nil {
		return "", "", false, err
	}
	var name string
	var location httpconvention.Location
	for _, tag := range tags.Tags() {
		var current httpconvention.Location
		switch tag.Key {
		case "path":
			current = httpconvention.LocationPath
		case "form":
			current = httpconvention.LocationQuery
		case "json":
			current = httpconvention.LocationBody
		default:
			continue
		}
		if name != "" {
			return "", "", false, errors.New("a field may declare only one transport tag")
		}
		if tag.Name == "" || tag.Name == "-" {
			return "", "", false, errors.New("transport tag requires a field name")
		}
		if tag.Name != sourceName && tag.Name != canonical && tag.Name != lowerSnakeName(canonical) {
			return "", "", false, fmt.Errorf("transport tag %q is not the source field identity", tag.Name)
		}
		if err := httpconvention.ValidateFieldName(tag.Name); err != nil {
			return "", "", false, err
		}
		for _, option := range tag.Options {
			if option != "optional" && option != "omitempty" {
				return "", "", false, fmt.Errorf("transport tag option %q is not supported", option)
			}
		}
		name, location = tag.Name, current
	}
	if name == "" {
		if err := httpconvention.ValidateFieldName(sourceName); err == nil {
			return sourceName, "", false, nil
		}
		return canonical, "", false, nil
	}
	return name, location, true, nil
}

func lowerSnakeName(lowerCamel string) string {
	var result strings.Builder
	for index, character := range lowerCamel {
		if index > 0 && unicode.IsUpper(character) {
			result.WriteByte('_')
		}
		result.WriteRune(unicode.ToLower(character))
	}
	return result.String()
}
