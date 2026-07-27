package main

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"go/format"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"unicode"

	"github.com/nxnminieye/nexa/generation/httpapi"
	"github.com/nxnminieye/nexa/provenance"
	apiFormat "github.com/zeromicro/go-zero/tools/goctl/pkg/parser/api/format"
)

const version = "dg2-typed-http-renderer v1.0.0"

type wireSource struct {
	Ref    string `json:"ref"`
	Digest string `json:"digest"`
}

type wireProvenance struct {
	Kind    string   `json:"kind"`
	Sources []string `json:"sources"`
}

type wireValue struct {
	Kind    string     `json:"kind"`
	Name    string     `json:"name,omitempty"`
	Element *wireValue `json:"element,omitempty"`
	Key     *wireValue `json:"key,omitempty"`
	Value   *wireValue `json:"value,omitempty"`
}

type wireBinding struct {
	Location string `json:"in"`
	Name     string `json:"name"`
}

type wireOrigin struct {
	Ref    string `json:"ref"`
	Digest string `json:"digest"`
}

type wireField struct {
	Path       []string       `json:"path"`
	Required   bool           `json:"required"`
	ValueType  wireValue      `json:"valueType"`
	Binding    *wireBinding   `json:"binding,omitempty"`
	Origin     *wireOrigin    `json:"origin,omitempty"`
	Provenance wireProvenance `json:"provenance"`
}

type wireType struct {
	Name       string         `json:"name"`
	Shape      wireValue      `json:"shape"`
	Fields     []wireField    `json:"fields"`
	Provenance wireProvenance `json:"provenance"`
}

type wireCredential struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Location string `json:"in"`
	Name     string `json:"name"`
}

type wireAuth struct {
	Mode        string           `json:"mode"`
	Credentials []wireCredential `json:"credentials"`
}

type wireCapability struct {
	ID         string `json:"id"`
	APIVersion string `json:"apiVersion"`
}

type wireOperation struct {
	ID               string            `json:"id"`
	Method           string            `json:"method"`
	Path             string            `json:"path"`
	RequestType      string            `json:"requestType"`
	ResponseBody     string            `json:"responseBody"`
	ResponseType     string            `json:"responseType,omitempty"`
	Auth             wireAuth          `json:"auth"`
	Permission       string            `json:"permission"`
	Capability       *wireCapability   `json:"capability,omitempty"`
	ErrorProjections []json.RawMessage `json:"errorProjections"`
	Provenance       wireProvenance    `json:"provenance"`
}

type wireDocument struct {
	APIVersion   string          `json:"apiVersion"`
	Kind         string          `json:"kind"`
	SourceDigest string          `json:"sourceDigest"`
	Sources      []wireSource    `json:"sources"`
	Types        []wireType      `json:"types"`
	Operations   []wireOperation `json:"operations"`
}

func main() {
	if len(os.Args) == 2 && os.Args[1] == "version" {
		fmt.Println(version)
		return
	}
	if len(os.Args) != 7 || os.Args[1] != "api" || os.Args[2] != "generate" || os.Args[3] != "--service" || os.Args[4] != "core" || os.Args[5] != "--generated-scope" {
		fatal("invalid arguments")
	}
	input, err := io.ReadAll(os.Stdin)
	if err != nil || len(bytes.TrimSpace(input)) == 0 {
		fatal("invalid canonical HTTP API input")
	}
	source, err := provenance.ParseDomainSource("generated/http-api-ir.json")
	if err != nil {
		fatal(err.Error())
	}
	if _, err := httpapi.ParseSnapshot(source, input); err != nil {
		fatal("invalid canonical HTTP API input: " + err.Error())
	}
	decoder := json.NewDecoder(bytes.NewReader(input))
	decoder.DisallowUnknownFields()
	var document wireDocument
	if err := decoder.Decode(&document); err != nil {
		fatal("invalid typed HTTP API input: " + err.Error())
	}
	if err := ensureEOF(decoder); err != nil {
		fatal("invalid typed HTTP API input: " + err.Error())
	}
	if err := validateTypedDocument(document); err != nil {
		fatal("invalid typed HTTP API input: " + err.Error())
	}
	scope := os.Args[6]
	if scope == "" || filepath.IsAbs(scope) || filepath.Clean(scope) != scope || !filepath.IsLocal(scope) {
		fatal("invalid generated scope")
	}
	apiSource, err := renderAPI(document)
	if err != nil {
		fatal(err.Error())
	}
	clientSource, err := renderClient(document)
	if err != nil {
		fatal(err.Error())
	}
	for name, content := range map[string][]byte{
		"account.generated.api": apiSource,
		"client.generated.go":   clientSource,
	} {
		target := filepath.Join(scope, name)
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			fatal(err.Error())
		}
		if err := os.WriteFile(target, content, 0o644); err != nil {
			fatal(err.Error())
		}
	}
}

func ensureEOF(decoder *json.Decoder) error {
	var extra any
	if err := decoder.Decode(&extra); err == io.EOF {
		return nil
	} else {
		return err
	}
}

func validateTypedDocument(document wireDocument) error {
	if document.APIVersion != httpapi.APIVersion || document.Kind != httpapi.Kind {
		return fmt.Errorf("unexpected document identity")
	}
	types := make(map[string]wireType, len(document.Types))
	for _, item := range document.Types {
		if item.Name == "" || item.Shape.Kind != "object" || item.Shape.Name != "" || item.Shape.Element != nil || item.Shape.Key != nil || item.Shape.Value != nil {
			return fmt.Errorf("invalid object type %q", item.Name)
		}
		if _, exists := types[item.Name]; exists {
			return fmt.Errorf("duplicate type %q", item.Name)
		}
		types[item.Name] = item
		fields := map[string]bool{}
		for _, field := range item.Fields {
			if len(field.Path) != 1 || field.Path[0] == "" || fields[field.Path[0]] {
				return fmt.Errorf("invalid field path in %q", item.Name)
			}
			fields[field.Path[0]] = true
		}
	}
	for _, item := range document.Types {
		for _, field := range item.Fields {
			if err := validateValue(field.ValueType, types, map[string]bool{item.Name: true}); err != nil {
				return fmt.Errorf("type %s field %s: %w", item.Name, field.Path[0], err)
			}
		}
	}
	operations := map[string]bool{}
	for _, operation := range document.Operations {
		if operation.ID == "" || operations[operation.ID] {
			return fmt.Errorf("invalid operation id %q", operation.ID)
		}
		operations[operation.ID] = true
		if _, ok := types[operation.RequestType]; operation.RequestType == "" || !ok {
			return fmt.Errorf("operation %s has invalid request type %q", operation.ID, operation.RequestType)
		}
		if operation.ResponseBody == "json" {
			if _, ok := types[operation.ResponseType]; operation.ResponseType == "" || !ok {
				return fmt.Errorf("operation %s has invalid response type %q", operation.ID, operation.ResponseType)
			}
		}
	}
	if !operations["account.replace"] {
		return fmt.Errorf("account.replace operation is missing")
	}
	return nil
}

func validateValue(value wireValue, types map[string]wireType, stack map[string]bool) error {
	switch value.Kind {
	case "scalar":
		if !scalarNames[value.Name] || value.Element != nil || value.Key != nil || value.Value != nil {
			return fmt.Errorf("invalid scalar %q", value.Name)
		}
	case "ref":
		if value.Name == "" || value.Element != nil || value.Key != nil || value.Value != nil {
			return fmt.Errorf("invalid ref shape")
		}
		target, ok := types[value.Name]
		if !ok {
			return fmt.Errorf("unknown ref %q", value.Name)
		}
		if stack[value.Name] {
			return fmt.Errorf("recursive ref %q", value.Name)
		}
		next := cloneSet(stack)
		next[value.Name] = true
		for _, field := range target.Fields {
			if err := validateValue(field.ValueType, types, next); err != nil {
				return err
			}
		}
	case "array", "optional":
		if value.Name != "" || value.Element == nil || value.Key != nil || value.Value != nil {
			return fmt.Errorf("invalid %s shape", value.Kind)
		}
		if err := validateValue(*value.Element, types, stack); err != nil {
			return err
		}
	default:
		return fmt.Errorf("unsupported value kind %q", value.Kind)
	}
	return nil
}

func cloneSet(input map[string]bool) map[string]bool {
	result := make(map[string]bool, len(input))
	for key, value := range input {
		result[key] = value
	}
	return result
}

var scalarNames = map[string]bool{
	"bool": true, "bytes": true, "float32": true, "float64": true,
	"int32": true, "int64": true, "string": true, "uint32": true, "uint64": true,
}

func renderAPI(document wireDocument) ([]byte, error) {
	var source strings.Builder
	source.WriteString("syntax = \"v1\"\n\ninfo (\n  nexaContractVersion: \"nexa.dev/http-api/v1\"\n)\n\n")
	for _, item := range document.Types {
		source.WriteString("type " + item.Name + " {\n")
		for _, field := range item.Fields {
			typeText, err := apiType(field.ValueType)
			if err != nil {
				return nil, err
			}
			source.WriteString("  " + field.Path[0] + " " + typeText)
			if field.Binding != nil {
				key := map[string]string{"path": "path", "query": "form", "header": "header", "body": "json"}[field.Binding.Location]
				if key == "" {
					return nil, fmt.Errorf("unsupported binding location %q", field.Binding.Location)
				}
				value := field.Binding.Name
				if !field.Required {
					value += ",optional"
				}
				source.WriteString(" `" + key + ":" + strconv.Quote(value) + "`")
			}
			source.WriteByte('\n')
		}
		source.WriteString("}\n\n")
	}
	for _, operation := range document.Operations {
		source.WriteString("@server (\n")
		metadata := [][2]string{{"nexaOperationId", operation.ID}, {"nexaAuthMode", operation.Auth.Mode}}
		if operation.Permission != "" {
			metadata = append(metadata, [2]string{"nexaPermission", operation.Permission})
		}
		if operation.Capability != nil {
			metadata = append(metadata, [2]string{"nexaCapabilityId", operation.Capability.ID}, [2]string{"nexaCapabilityVersion", operation.Capability.APIVersion})
		}
		for _, item := range metadata {
			source.WriteString("  " + item[0] + ": " + strconv.Quote(item[1]) + "\n")
		}
		source.WriteString(")\nservice generated-api {\n  @handler generated" + hex.EncodeToString([]byte(operation.ID)) + "\n  ")
		source.WriteString(strings.ToLower(operation.Method) + " " + routePath(operation.Path))
		if operation.RequestType != "" {
			source.WriteString(" (" + operation.RequestType + ")")
		}
		if operation.ResponseBody == "json" {
			source.WriteString(" returns (" + operation.ResponseType + ")")
		}
		source.WriteString("\n}\n\n")
	}
	var output bytes.Buffer
	if err := apiFormat.Source([]byte(source.String()), &output); err != nil {
		return nil, fmt.Errorf("format generated API: %w", err)
	}
	return output.Bytes(), nil
}

func renderClient(document wireDocument) ([]byte, error) {
	var source strings.Builder
	source.WriteString("package generated\n\nimport \"context\"\n\n")
	for _, item := range document.Types {
		source.WriteString("type " + item.Name + " struct {\n")
		for _, field := range item.Fields {
			typeText, err := goType(field.ValueType)
			if err != nil {
				return nil, err
			}
			jsonName := lowerFirst(field.Path[0])
			if field.Binding != nil && field.Binding.Name != "" {
				jsonName = field.Binding.Name
			}
			source.WriteString(field.Path[0] + " " + typeText + " `json:\"" + jsonName + "\"`\n")
		}
		source.WriteString("}\n")
	}
	source.WriteString("type RPCClient interface {\n")
	for _, operation := range document.Operations {
		prefix := exported(operation.ID)
		source.WriteString(prefix + "(context.Context, " + operation.RequestType + ") (" + operation.ResponseType + ", error)\n")
		source.WriteString("}\n")
		source.WriteString("type " + prefix + "RPCRequest = " + operation.RequestType + "\n")
		if operation.ResponseType != "" {
			source.WriteString("type " + prefix + "RPCResponse = " + operation.ResponseType + "\n")
		}
	}
	formatted, err := format.Source([]byte(source.String()))
	if err != nil {
		return nil, fmt.Errorf("format generated client: %w", err)
	}
	return formatted, nil
}

func apiType(value wireValue) (string, error) {
	switch value.Kind {
	case "scalar", "ref":
		return value.Name, nil
	case "array":
		inner, err := apiType(*value.Element)
		return "[]" + inner, err
	case "optional":
		inner, err := apiType(*value.Element)
		return "*" + inner, err
	default:
		return "", fmt.Errorf("unsupported API value kind %q", value.Kind)
	}
}

func goType(value wireValue) (string, error) {
	if value.Kind == "scalar" && value.Name == "bytes" {
		return "[]byte", nil
	}
	return apiType(value)
}

func routePath(value string) string {
	segments := strings.Split(value, "/")
	for index, segment := range segments {
		if strings.HasPrefix(segment, "{") && strings.HasSuffix(segment, "}") {
			segments[index] = ":" + strings.TrimSuffix(strings.TrimPrefix(segment, "{"), "}")
		}
	}
	return strings.Join(segments, "/")
}

func exported(value string) string {
	var result strings.Builder
	upper := true
	for _, current := range value {
		if !unicode.IsLetter(current) && !unicode.IsDigit(current) {
			upper = true
			continue
		}
		if upper {
			current = unicode.ToUpper(current)
			upper = false
		}
		result.WriteRune(current)
	}
	return result.String()
}

func lowerFirst(value string) string {
	if value == "" {
		return value
	}
	runes := []rune(value)
	runes[0] = unicode.ToLower(runes[0])
	return string(runes)
}

func fatal(message string) {
	fmt.Fprintln(os.Stderr, message)
	os.Exit(1)
}
