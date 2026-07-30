package sourcecomment

import (
	"fmt"
	"strings"

	"github.com/nxnminieye/nexa/generation/httpconvention"
)

// CanonicalRPCOperationID preserves the complete catalog and Proto RPC
// identity while applying the single HTTP Convention naming function.
func CanonicalRPCOperationID(serviceID, methodFullName string) (string, error) {
	segments := append([]string{serviceID}, strings.Split(methodFullName, ".")...)
	if serviceID == "" || methodFullName == "" {
		return "", fmt.Errorf("RPC operation identity is incomplete")
	}
	for index, segment := range segments {
		canonical, err := canonicalOperationSegment(segment)
		if err != nil {
			return "", fmt.Errorf("RPC identity segment %q: %w", segment, err)
		}
		segments[index] = canonical
	}
	return strings.Join(segments, "."), nil
}

// CanonicalAPIOperationID follows go-zero's group/handler uniqueness boundary.
func CanonicalAPIOperationID(group, handler string) (string, error) {
	handlerSegment, err := canonicalOperationSegment(handler)
	if err != nil {
		return "", fmt.Errorf("handler %q: %w", handler, err)
	}
	if group == "" {
		return handlerSegment, nil
	}
	groupSegment, err := canonicalOperationSegment(group)
	if err != nil {
		return "", fmt.Errorf("group %q: %w", group, err)
	}
	return groupSegment + "." + handlerSegment, nil
}

func canonicalOperationSegment(value string) (string, error) {
	return httpconvention.CanonicalName(strings.ReplaceAll(value, "-", "_"))
}
