package httpapi

import (
	"bytes"
	"encoding/hex"
	"fmt"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"

	"github.com/nxnminieye/nexa/generation/api"
	"github.com/zeromicro/go-zero/tools/goctl/api/spec"
	"github.com/zeromicro/go-zero/tools/goctl/pkg/parser/api/format"
	goctlparser "github.com/zeromicro/go-zero/tools/goctl/pkg/parser/api/parser"
)

func RenderGenerated(document Document) ([]byte, error) {
	if document.state == nil || !documentHasOnly(document, NodeFactGenerated) {
		return nil, invalid("render_input_invalid", "", "", "render requires a generated HTTP API document")
	}
	var source strings.Builder
	source.WriteString("syntax = \"v1\"\n\ninfo (\n  nexaContractVersion: \"nexa.dev/http-api/v1\"\n)\n\n")
	for _, item := range document.state.types {
		source.WriteString("type " + item.name + " {\n")
		for _, field := range item.fields {
			if len(field.path) != 1 {
				return nil, invalid("render_nested_field_unsupported", "", "", "generated nested fields must be represented by named types")
			}
			typeText, err := renderValueType(field.valueType)
			if err != nil {
				return nil, err
			}
			source.WriteString("  " + field.path[0] + " " + typeText)
			tags := renderFieldTags(field)
			if tags != "" {
				source.WriteString(" `" + tags + "`")
			}
			source.WriteByte('\n')
		}
		source.WriteString("}\n\n")
	}
	for _, operation := range document.state.operations {
		source.WriteString("@server (\n")
		metadata := [][2]string{{"nexaOperationId", operation.id}, {"nexaAuthMode", string(operation.auth.mode)}}
		if len(operation.auth.credentials) == 1 {
			credential := operation.auth.credentials[0]
			metadata = append(metadata, [2]string{"nexaCredentialId", credential.id}, [2]string{"nexaCredentialType", string(credential.typeID)}, [2]string{"nexaCredentialLocation", string(credential.location)}, [2]string{"nexaCredentialName", credential.name})
		}
		if operation.permission != "" {
			metadata = append(metadata, [2]string{"nexaPermission", operation.permission})
		}
		if operation.hasCapability {
			metadata = append(metadata, [2]string{"nexaCapabilityId", operation.capability.id}, [2]string{"nexaCapabilityVersion", operation.capability.apiVersion})
		}
		for _, item := range metadata {
			source.WriteString("  " + item[0] + ": " + strconv.Quote(item[1]) + "\n")
		}
		source.WriteString(")\nservice generated-api {\n  @handler generated" + hex.EncodeToString([]byte(operation.id)) + "\n  ")
		source.WriteString(strings.ToLower(string(operation.method)) + " " + renderRoutePath(operation.path))
		if operation.requestType != "" {
			source.WriteString(" (" + operation.requestType + ")")
		}
		if operation.responseBody == api.ResponseBodyJSON {
			source.WriteString(" returns (" + operation.responseType + ")")
		}
		source.WriteString("\n}\n\n")
	}
	var formatted bytes.Buffer
	if err := format.Source([]byte(source.String()), &formatted); err != nil {
		return nil, invalid("render_invalid", "", "", err.Error())
	}
	return formatted.Bytes(), nil
}

func VerifyRenderedGenerated(source string, rendered []byte, expected Document) error {
	if source == "" {
		return invalid("verify_source_invalid", source, "", "rendered source name is required")
	}
	virtual := filepath.Join(filepath.Clean(string(filepath.Separator)+"nexa-httpapi-verify"), filepath.Base(source))
	actual, err := goctlparser.Parse(virtual, rendered)
	if err != nil {
		return invalid("rendered_parser_error", source, "", err.Error())
	}
	if err := actual.Validate(); err != nil {
		return invalid("rendered_validation_failed", source, "", err.Error())
	}
	expectedBytes, err := RenderGenerated(expected)
	if err != nil {
		return err
	}
	want, err := goctlparser.Parse(virtual, expectedBytes)
	if err != nil {
		return invalid("rendered_expected_invalid", source, "", err.Error())
	}
	if !reflect.DeepEqual(renderedSemantics(actual), renderedSemantics(want)) {
		return invalid("rendered_semantic_drift", source, "", "rendered HTTP API semantics differ from expected APIIR")
	}
	return nil
}

type renderedType struct {
	Name   string
	Fields []renderedField
}
type renderedField struct {
	Name, Type string
	Tags       [][2]string
}
type renderedRoute struct {
	Metadata                        [][2]string
	Method, Path, Request, Response string
}
type renderedDocument struct {
	Types  []renderedType
	Routes []renderedRoute
}

func renderedSemantics(input *spec.ApiSpec) renderedDocument {
	result := renderedDocument{}
	for _, value := range input.Types {
		structure, ok := value.(spec.DefineStruct)
		if !ok {
			continue
		}
		item := renderedType{Name: structure.RawName}
		for _, member := range structure.Members {
			field := renderedField{Name: member.Name, Type: semanticType(member.Type)}
			if tags, err := spec.Parse(member.Tag); err == nil {
				for _, tag := range tags.Tags() {
					field.Tags = append(field.Tags, [2]string{tag.Key, tag.Name + "\x00" + strings.Join(tag.Options, ",")})
				}
				sort.Slice(field.Tags, func(i, j int) bool { return field.Tags[i][0] < field.Tags[j][0] })
			}
			item.Fields = append(item.Fields, field)
		}
		result.Types = append(result.Types, item)
	}
	for _, group := range input.Service.Groups {
		metadata := make([][2]string, 0, len(group.Annotation.Properties))
		for key, value := range group.Annotation.Properties {
			if strings.HasPrefix(key, "nexa") {
				metadata = append(metadata, [2]string{key, value})
			}
		}
		sort.Slice(metadata, func(i, j int) bool { return metadata[i][0] < metadata[j][0] })
		for _, route := range group.Routes {
			item := renderedRoute{Metadata: append([][2]string(nil), metadata...), Method: strings.ToUpper(route.Method), Path: route.Path}
			if route.RequestType != nil {
				item.Request = route.RequestType.Name()
			}
			if route.ResponseType != nil {
				item.Response = route.ResponseType.Name()
			}
			result.Routes = append(result.Routes, item)
		}
	}
	return result
}

func semanticType(value spec.Type) string {
	switch typed := value.(type) {
	case spec.PrimitiveType:
		return "scalar:" + typed.RawName
	case spec.DefineStruct:
		return "ref:" + typed.RawName
	case spec.PointerType:
		return "optional:" + semanticType(typed.Type)
	case spec.ArrayType:
		return "array:" + semanticType(typed.Value)
	case spec.MapType:
		return "map:" + typed.Key + ":" + semanticType(typed.Value)
	case spec.NestedStruct:
		return "object"
	default:
		return fmt.Sprintf("%T", value)
	}
}
func renderValueType(value ValueType) (string, error) {
	switch value.kind {
	case ValueScalar, ValueRef:
		return value.name, nil
	case ValueOptional:
		if value.element == nil {
			return "", invalid("value_type_invalid", "", "", "optional element missing")
		}
		inner, err := renderValueType(*value.element)
		return "*" + inner, err
	case ValueArray:
		if value.element == nil {
			return "", invalid("value_type_invalid", "", "", "array element missing")
		}
		inner, err := renderValueType(*value.element)
		return "[]" + inner, err
	case ValueMap:
		if value.key == nil || value.value == nil {
			return "", invalid("value_type_invalid", "", "", "map type missing")
		}
		key, err := renderValueType(*value.key)
		if err != nil {
			return "", err
		}
		item, err := renderValueType(*value.value)
		return "map[" + key + "]" + item, err
	default:
		return "", invalid("value_type_invalid", "", "", "object values must use a named type")
	}
}
func renderFieldTags(field *fieldState) string {
	var tags []string
	if field.hasBinding {
		key := map[api.RequestBindingLocation]string{api.RequestBindingPath: "path", api.RequestBindingQuery: "form", api.RequestBindingHeader: "header", api.RequestBindingBody: "json"}[field.binding.location]
		value := field.binding.name
		if !field.required {
			value += ",optional"
		}
		tags = append(tags, key+":"+strconv.Quote(value))
	}
	if field.hasOrigin {
		tags = append(tags, originRefTag+":"+strconv.Quote(field.origin.Ref.String()), originDigestTag+":"+strconv.Quote(field.origin.Digest.String()))
	}
	return strings.Join(tags, " ")
}
func renderRoutePath(value string) string {
	segments := strings.Split(value, "/")
	for index, segment := range segments {
		if strings.HasPrefix(segment, "{") && strings.HasSuffix(segment, "}") {
			segments[index] = ":" + strings.TrimSuffix(strings.TrimPrefix(segment, "{"), "}")
		}
	}
	return strings.Join(segments, "/")
}
