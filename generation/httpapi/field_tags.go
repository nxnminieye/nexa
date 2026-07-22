package httpapi

import (
	"fmt"
	"strings"

	"github.com/nxnminieye/nexa/generation/api"
	"github.com/nxnminieye/nexa/provenance"
	"github.com/zeromicro/go-zero/tools/goctl/api/spec"
)

const (
	originRefTag    = "nexaOriginRef"
	originDigestTag = "nexaOriginDigest"
)

func parseFieldTags(raw string) (Binding, bool, provenance.Source, bool, bool, error) {
	if raw == "" {
		return Binding{}, false, provenance.Source{}, false, false, nil
	}
	tags, err := spec.Parse(raw)
	if err != nil {
		return Binding{}, false, provenance.Source{}, false, false, err
	}
	seen := map[string]bool{}
	var binding Binding
	hasBinding, optional := false, false
	var refValue, digestValue string
	for _, tag := range tags.Tags() {
		if seen[tag.Key] {
			return Binding{}, false, provenance.Source{}, false, false, fmt.Errorf("duplicate struct tag %q", tag.Key)
		}
		seen[tag.Key] = true
		switch tag.Key {
		case "path", "form", "header", "json":
			if hasBinding {
				return Binding{}, false, provenance.Source{}, false, false, fmt.Errorf("field has multiple request bindings")
			}
			if tag.Name == "" || tag.Name == "-" {
				return Binding{}, false, provenance.Source{}, false, false, fmt.Errorf("binding name is required")
			}
			location := api.RequestBindingBody
			switch tag.Key {
			case "path":
				location = api.RequestBindingPath
			case "form":
				location = api.RequestBindingQuery
			case "header":
				location = api.RequestBindingHeader
			}
			name := tag.Name
			if location == api.RequestBindingHeader {
				name = strings.ToLower(name)
			}
			binding, hasBinding = Binding{location: location, name: name}, true
			for _, option := range tag.Options {
				if option == "optional" || option == "omitempty" {
					optional = true
				}
			}
		case originRefTag:
			refValue = tag.Name
		case originDigestTag:
			digestValue = tag.Name
		default:
			if strings.HasPrefix(tag.Key, "nexa") {
				return Binding{}, false, provenance.Source{}, false, false, fmt.Errorf("unknown Nexa struct tag %q", tag.Key)
			}
		}
	}
	if (refValue == "") != (digestValue == "") {
		return Binding{}, false, provenance.Source{}, false, false, fmt.Errorf("field origin tags must be declared together")
	}
	if refValue == "" {
		return binding, hasBinding, provenance.Source{}, false, optional, nil
	}
	ref, err := provenance.ParseSourceRef(refValue)
	if err != nil {
		return Binding{}, false, provenance.Source{}, false, false, err
	}
	digest, err := provenance.ParseDigest(digestValue)
	if err != nil {
		return Binding{}, false, provenance.Source{}, false, false, err
	}
	return binding, hasBinding, provenance.Source{Ref: ref, Digest: digest}, true, optional, nil
}
