package sourcecomment

import (
	"bytes"
	"encoding/json"
	"io"
	"regexp"
	"strings"
	"unicode/utf8"
)

const (
	MaxDirectiveBytes = 16 << 10
	MaxListItems      = 128
	MaxValueDepth     = 4
)

var keyPattern = regexp.MustCompile(`^(?:\$[a-z][A-Za-z0-9]*|[a-z][A-Za-z0-9]*(?:\.[A-Za-z][A-Za-z0-9-]*)*)$`)

type Line struct {
	Text          string
	CommentPrefix string
	Location      Location
	Target        *Target
}

type ParsedFile struct {
	contract string
	facts    []BoundFact
	sources  []BoundSource
}

func (f ParsedFile) Contract() string   { return f.contract }
func (f ParsedFile) Facts() []BoundFact { return cloneBoundFacts(f.facts) }
func (f ParsedFile) Sources() []BoundSource {
	return append([]BoundSource(nil), f.sources...)
}

type BoundFact struct {
	target    Target
	directive Directive
}

func (f BoundFact) Target() Target       { return f.target }
func (f BoundFact) Directive() Directive { return cloneDirective(f.directive) }

type BoundSource struct {
	target   Target
	source   SourceRef
	location Location
}

func (s BoundSource) Target() Target     { return s.target }
func (s BoundSource) Source() SourceRef  { return s.source }
func (s BoundSource) Location() Location { return s.location }

func ParseLine(line Line) (Directive, bool, *Diagnostic) {
	location := line.Location
	if location.Line <= 0 {
		location.Line = 1
	}
	if location.Column <= 0 {
		location.Column = 1
	}
	if line.CommentPrefix != "//" && line.CommentPrefix != "#" {
		value := diagnostic(CodeInvalidSyntax, location, "use a v1 line-comment carrier prefix")
		return Directive{}, false, &value
	}
	trimmed := strings.TrimLeft(line.Text, " \t")
	indent := len(line.Text) - len(trimmed)
	if !strings.HasPrefix(trimmed, line.CommentPrefix) {
		return Directive{}, false, nil
	}
	remainder := strings.TrimPrefix(trimmed, line.CommentPrefix)
	if !strings.HasPrefix(remainder, " @nexa") {
		return Directive{}, false, nil
	}
	location.Column += indent + len(line.CommentPrefix) + 1
	if len(line.Text) > MaxDirectiveBytes || !utf8.ValidString(line.Text) {
		value := diagnostic(CodeInvalidSyntax, location, "use a valid UTF-8 directive within the v1 length limit")
		return Directive{}, false, &value
	}
	if !strings.HasPrefix(remainder, " @nexa ") {
		value := diagnostic(CodeInvalidSyntax, location, "use '<comment-prefix> @nexa <key>: <json-value>'")
		return Directive{}, false, &value
	}
	payload := strings.TrimPrefix(remainder, " @nexa ")
	separator := strings.Index(payload, ": ")
	if separator <= 0 {
		value := diagnostic(CodeInvalidSyntax, location, "separate the key and JSON value with ': '")
		return Directive{}, false, &value
	}
	key, raw := payload[:separator], payload[separator+2:]
	if !keyPattern.MatchString(key) || raw == "" {
		value := diagnostic(CodeInvalidSyntax, location, "use an exact ASCII v1 key and a non-empty JSON value")
		return Directive{}, false, &value
	}
	var parsed Value
	var err error
	if key == "ui.reference" {
		parsed, err = decodeReference([]byte(raw))
	} else {
		parsed, err = decodeValue([]byte(raw), 0)
	}
	if err != nil {
		value := diagnostic(CodeInvalidSyntax, location, "use a single-line JSON scalar or list; null and objects are not supported")
		return Directive{}, false, &value
	}
	return Directive{key: key, value: parsed, location: location}, true, nil
}

func decodeReference(raw []byte) (Value, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	opening, err := decoder.Token()
	if err != nil || opening != json.Delim('{') {
		return Value{}, io.ErrUnexpectedEOF
	}
	values := map[string]string{}
	for decoder.More() {
		token, err := decoder.Token()
		if err != nil {
			return Value{}, err
		}
		key, ok := token.(string)
		if !ok || (key != "target" && key != "display") || values[key] != "" {
			return Value{}, io.ErrUnexpectedEOF
		}
		var value string
		if err := decoder.Decode(&value); err != nil || value == "" {
			return Value{}, io.ErrUnexpectedEOF
		}
		values[key] = value
	}
	closing, err := decoder.Token()
	if err != nil || closing != json.Delim('}') || decoder.Decode(&struct{}{}) != io.EOF || values["target"] == "" || values["display"] == "" {
		return Value{}, io.ErrUnexpectedEOF
	}
	return ReferenceValue(values["target"], values["display"]), nil
}

func decodeValue(raw []byte, depth int) (Value, error) {
	if depth > MaxValueDepth {
		return Value{}, io.ErrUnexpectedEOF
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var decoded any
	if err := decoder.Decode(&decoded); err != nil {
		return Value{}, err
	}
	if decoder.Decode(&struct{}{}) != io.EOF {
		return Value{}, io.ErrUnexpectedEOF
	}
	return typedValue(decoded, depth)
}

func typedValue(input any, depth int) (Value, error) {
	switch value := input.(type) {
	case string:
		return StringValue(value), nil
	case bool:
		return BooleanValue(value), nil
	case json.Number:
		integer, ok := canonicalInteger(value)
		if !ok {
			return Value{}, io.ErrUnexpectedEOF
		}
		return IntegerValue(integer), nil
	case []any:
		if len(value) > MaxListItems || depth >= MaxValueDepth {
			return Value{}, io.ErrUnexpectedEOF
		}
		items := make([]Value, len(value))
		for index, item := range value {
			parsed, err := typedValue(item, depth+1)
			if err != nil {
				return Value{}, err
			}
			items[index] = parsed
		}
		return ListValue(items...), nil
	default:
		return Value{}, io.ErrUnexpectedEOF
	}
}

func ParseFile(registry Registry, path string, lines []Line) (ParsedFile, []Diagnostic) {
	result := ParsedFile{}
	var diagnostics []Diagnostic
	contractCount := 0
	hasDirective := false
	seen := map[string]Location{}
	for _, line := range lines {
		directive, selected, failure := ParseLine(line)
		if failure != nil {
			diagnostics = append(diagnostics, *failure)
			continue
		}
		if !selected {
			continue
		}
		hasDirective = true
		switch directive.key {
		case "$contract":
			contractCount++
			value, ok := directive.value.String()
			if line.Target != nil || !ok || value != Contract {
				item := diagnostic(CodeInvalidValue, directive.location, "declare the supported contract once at file scope")
				item.Expected, item.Actual = Contract, directive.value.display()
				diagnostics = append(diagnostics, item)
			} else {
				result.contract = value
			}
			if contractCount > 1 {
				diagnostics = append(diagnostics, diagnostic(CodeDuplicateFact, directive.location, "keep exactly one file-level $contract directive"))
			}
		case "$source":
			if line.Target == nil {
				diagnostics = append(diagnostics, diagnostic(CodeInvalidTarget, directive.location, "bind $source to a projected semantic node"))
				continue
			}
			raw, ok := directive.value.String()
			if !ok {
				diagnostics = append(diagnostics, diagnostic(CodeInvalidValue, directive.location, "use a quoted canonical source reference"))
				continue
			}
			ref, err := ParseSourceRef(raw)
			if err != nil {
				diagnostics = append(diagnostics, diagnostic(CodeInvalidValue, directive.location, "use <stage>://<repository-relative-path>#<semantic-symbol>"))
				continue
			}
			key := line.Target.Source.String() + "\x00$source"
			if _, duplicate := seen[key]; duplicate {
				diagnostics = append(diagnostics, diagnostic(CodeDuplicateFact, directive.location, "remove the duplicate $source directive"))
				continue
			}
			seen[key] = directive.location
			result.sources = append(result.sources, BoundSource{target: *line.Target, source: ref, location: directive.location})
		default:
			if strings.HasPrefix(directive.key, "$") {
				diagnostics = append(diagnostics, diagnostic(CodeUnknownKey, directive.location, "remove the unknown system key"))
				continue
			}
			entry, ok := registry.lookup(directive.key)
			if !ok {
				diagnostics = append(diagnostics, diagnostic(CodeUnknownKey, directive.location, "register the fact in the canonical Nexa contract before authoring it"))
				continue
			}
			if line.Target == nil {
				diagnostics = append(diagnostics, diagnostic(CodeInvalidTarget, directive.location, "bind the directive to an adapter-resolved semantic node"))
				continue
			}
			factID := line.Target.SemanticID + ":" + directive.key
			key := line.Target.Source.String() + "\x00" + directive.key
			if first, duplicate := seen[key]; duplicate {
				item := diagnostic(CodeDuplicateFact, directive.location, "remove the duplicate fact declaration")
				item.Node, item.FactID, item.EarliestSource = line.Target.SemanticID, factID, line.Target.Source.String()
				item.Expected = first.File
				diagnostics = append(diagnostics, item)
				continue
			}
			seen[key] = directive.location
			if code, suggestion := entry.validate(*line.Target, directive.value); code != "" {
				item := diagnostic(code, directive.location, suggestion)
				item.Node, item.FactID, item.EarliestSource, item.Actual = line.Target.SemanticID, factID, line.Target.Source.String(), directive.value.display()
				diagnostics = append(diagnostics, item)
				continue
			}
			directive.value = entry.canonicalize(directive.value)
			result.facts = append(result.facts, BoundFact{target: *line.Target, directive: directive})
		}
	}
	if contractCount == 0 && hasDirective {
		diagnostics = append(diagnostics, diagnostic(CodeInvalidSyntax, Location{File: path, Line: 1, Column: 1}, "add one file-level $contract directive before Nexa facts"))
	}
	sortDiagnostics(diagnostics)
	if len(diagnostics) > 0 {
		return ParsedFile{}, diagnostics
	}
	return result, diagnostics
}

func cloneDirective(input Directive) Directive { input.value = cloneValue(input.value); return input }
func cloneBoundFacts(input []BoundFact) []BoundFact {
	result := make([]BoundFact, len(input))
	for index, item := range input {
		result[index] = BoundFact{target: item.target, directive: cloneDirective(item.directive)}
	}
	return result
}
