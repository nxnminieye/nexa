package api

import (
	"fmt"
	"sort"
	"strings"
	"unicode/utf16"
	"unicode/utf8"

	"github.com/nxnminieye/nexa/cli/protocol"
)

// ParseRequest parses and normalizes a strict JSON request object.
func ParseRequest(data []byte) (Request, error) {
	limits := RuntimeLimits()
	if len(data) > limits.RequestRawBytes {
		return Request{}, newRequestError("size_limit_exceeded", "")
	}
	parser := requestParser{
		data:      data,
		maxDepth:  limits.JSONDepth,
		maxNodes:  limits.JSONNodes,
		semantics: limits.JSONSemantics(),
		newError:  newRequestError,
	}
	value, err := parser.parseValue("", parser.semantics.RootDepth())
	if err != nil {
		return Request{}, err
	}
	parser.skipWhitespace()
	if parser.offset != len(parser.data) {
		return Request{}, newRequestError("trailing_input", "")
	}
	root, ok := value.(requestObject)
	if !ok {
		return Request{}, newRequestError("root_object_required", "")
	}
	normalized := root.appendJSON(make([]byte, 0, len(data)))
	return Request{root: root, json: normalized}, nil
}

type requestParser struct {
	data      []byte
	offset    int
	nodes     int
	maxDepth  int
	maxNodes  int
	semantics JSONLimitSemantics
	allowNull bool
	newError  func(reason, pointer string) *Error
}

func (p *requestParser) parseValue(pointer string, depth int) (requestValue, error) {
	if p.semantics.Inclusive() && depth > p.maxDepth || !p.semantics.Inclusive() && depth >= p.maxDepth {
		return nil, p.failure("depth_limit_exceeded", pointer)
	}
	if p.semantics.CountsValues() && (depth != p.semantics.RootDepth() || p.semantics.CountsRoot()) {
		p.nodes++
		if p.nodes > p.maxNodes {
			return nil, p.failure("node_limit_exceeded", pointer)
		}
	}
	p.skipWhitespace()
	if p.offset >= len(p.data) {
		return nil, p.failure("invalid_json", pointer)
	}
	switch p.data[p.offset] {
	case '{':
		return p.parseObject(pointer, depth)
	case '[':
		return p.parseArray(pointer, depth)
	case '"':
		value, err := p.parseString(pointer)
		return requestString(value), err
	case 't':
		if p.consumeLiteral("true") {
			return requestBool(true), nil
		}
	case 'f':
		if p.consumeLiteral("false") {
			return requestBool(false), nil
		}
	case 'n':
		if p.consumeLiteral("null") {
			if p.allowNull {
				return requestNull{}, nil
			}
			return nil, p.failure("null_not_allowed", pointer)
		}
	default:
		if p.data[p.offset] == '-' || isDigit(p.data[p.offset]) {
			value, ok := p.parseNumber()
			if ok {
				return requestNumber(value), nil
			}
		}
	}
	return nil, p.failure("invalid_json", pointer)
}

func (p *requestParser) parseObject(pointer string, depth int) (requestObject, error) {
	p.offset++
	p.skipWhitespace()
	if p.consumeByte('}') {
		return requestObject{}, nil
	}
	seen := make(map[string]struct{})
	members := make(requestObject, 0)
	for {
		p.skipWhitespace()
		if p.offset >= len(p.data) || p.data[p.offset] != '"' {
			return nil, p.failure("invalid_json", pointer)
		}
		name, err := p.parseString(pointer)
		if err != nil {
			return nil, err
		}
		childPointer := pointer + "/" + escapeJSONPointer(name)
		if _, duplicate := seen[name]; duplicate {
			return nil, p.failure("duplicate_key", childPointer)
		}
		seen[name] = struct{}{}
		p.skipWhitespace()
		if !p.consumeByte(':') {
			return nil, p.failure("invalid_json", childPointer)
		}
		p.skipWhitespace()
		valueStart := p.offset
		value, err := p.parseValue(childPointer, depth+1)
		if err != nil {
			return nil, err
		}
		members = append(members, requestMember{name: name, value: value, start: valueStart, end: p.offset})
		p.skipWhitespace()
		if p.consumeByte('}') {
			break
		}
		if !p.consumeByte(',') {
			return nil, p.failure("invalid_json", pointer)
		}
	}
	sort.Slice(members, func(i, j int) bool { return lessUTF16(members[i].name, members[j].name) })
	return members, nil
}

func (p *requestParser) parseArray(pointer string, depth int) (requestArray, error) {
	p.offset++
	p.skipWhitespace()
	if p.consumeByte(']') {
		return requestArray{}, nil
	}
	values := make(requestArray, 0)
	for index := 0; ; index++ {
		childPointer := fmt.Sprintf("%s/%d", pointer, index)
		value, err := p.parseValue(childPointer, depth+1)
		if err != nil {
			return nil, err
		}
		values = append(values, value)
		p.skipWhitespace()
		if p.consumeByte(']') {
			return values, nil
		}
		if !p.consumeByte(',') {
			return nil, p.failure("invalid_json", pointer)
		}
	}
}

func (p *requestParser) parseString(pointer string) (string, error) {
	p.offset++
	var value strings.Builder
	for p.offset < len(p.data) {
		character := p.data[p.offset]
		p.offset++
		switch character {
		case '"':
			return value.String(), nil
		case '\\':
			if p.offset >= len(p.data) {
				return "", p.failure("invalid_json", pointer)
			}
			escaped := p.data[p.offset]
			p.offset++
			switch escaped {
			case '"', '\\', '/':
				value.WriteByte(escaped)
			case 'b':
				value.WriteByte('\b')
			case 'f':
				value.WriteByte('\f')
			case 'n':
				value.WriteByte('\n')
			case 'r':
				value.WriteByte('\r')
			case 't':
				value.WriteByte('\t')
			case 'u':
				decoded, err := p.parseUnicodeEscape(pointer)
				if err != nil {
					return "", err
				}
				value.WriteRune(decoded)
			default:
				return "", p.failure("invalid_json", pointer)
			}
		default:
			if character < 0x20 {
				return "", p.failure("invalid_json", pointer)
			}
			if character < utf8.RuneSelf {
				value.WriteByte(character)
				continue
			}
			p.offset--
			r, size := utf8.DecodeRune(p.data[p.offset:])
			if r == utf8.RuneError && size == 1 {
				return "", p.failure("invalid_utf8", pointer)
			}
			value.Write(p.data[p.offset : p.offset+size])
			p.offset += size
		}
	}
	return "", p.failure("invalid_json", pointer)
}

func (p *requestParser) parseUnicodeEscape(pointer string) (rune, error) {
	first, ok := p.readHex4()
	if !ok {
		return 0, p.failure("invalid_json", pointer)
	}
	if first >= 0xdc00 && first <= 0xdfff {
		return 0, p.failure("invalid_unicode_scalar", pointer)
	}
	if first < 0xd800 || first > 0xdbff {
		return rune(first), nil
	}
	if p.offset+2 > len(p.data) || p.data[p.offset] != '\\' || p.data[p.offset+1] != 'u' {
		return 0, p.failure("invalid_unicode_scalar", pointer)
	}
	p.offset += 2
	second, ok := p.readHex4()
	if !ok || second < 0xdc00 || second > 0xdfff {
		return 0, p.failure("invalid_unicode_scalar", pointer)
	}
	return utf16.DecodeRune(rune(first), rune(second)), nil
}

func (p *requestParser) readHex4() (uint16, bool) {
	if p.offset+4 > len(p.data) {
		return 0, false
	}
	var result uint16
	for end := p.offset + 4; p.offset < end; p.offset++ {
		value, ok := hexValue(p.data[p.offset])
		if !ok {
			return 0, false
		}
		result = result<<4 | uint16(value)
	}
	return result, true
}

func (p *requestParser) parseNumber() (string, bool) {
	start := p.offset
	if p.consumeByte('-') && p.offset == len(p.data) {
		return "", false
	}
	if p.consumeByte('0') {
		if p.offset < len(p.data) && isDigit(p.data[p.offset]) {
			return "", false
		}
	} else {
		if p.offset >= len(p.data) || p.data[p.offset] < '1' || p.data[p.offset] > '9' {
			return "", false
		}
		for p.offset < len(p.data) && isDigit(p.data[p.offset]) {
			p.offset++
		}
	}
	if p.consumeByte('.') {
		if p.offset >= len(p.data) || !isDigit(p.data[p.offset]) {
			return "", false
		}
		for p.offset < len(p.data) && isDigit(p.data[p.offset]) {
			p.offset++
		}
	}
	if p.offset < len(p.data) && (p.data[p.offset] == 'e' || p.data[p.offset] == 'E') {
		p.offset++
		if p.offset < len(p.data) && (p.data[p.offset] == '+' || p.data[p.offset] == '-') {
			p.offset++
		}
		if p.offset >= len(p.data) || !isDigit(p.data[p.offset]) {
			return "", false
		}
		for p.offset < len(p.data) && isDigit(p.data[p.offset]) {
			p.offset++
		}
	}
	return string(p.data[start:p.offset]), true
}

func (p *requestParser) skipWhitespace() {
	for p.offset < len(p.data) {
		switch p.data[p.offset] {
		case ' ', '\t', '\n', '\r':
			p.offset++
		default:
			return
		}
	}
}

func (p *requestParser) consumeLiteral(value string) bool {
	if !strings.HasPrefix(string(p.data[p.offset:]), value) {
		return false
	}
	p.offset += len(value)
	return true
}

func (p *requestParser) consumeByte(value byte) bool {
	if p.offset >= len(p.data) || p.data[p.offset] != value {
		return false
	}
	p.offset++
	return true
}

func (p *requestParser) failure(reason, pointer string) *Error {
	return p.newError(reason, pointer)
}

func newRequestError(reason, pointer string) *Error {
	return newSDKError(
		codeRequestInvalid,
		sdkErrorDomain,
		protocol.CategoryInput,
		"request JSON is invalid",
		ErrorDetails{reason: reason, pointer: pointer},
	)
}

func escapeJSONPointer(value string) string {
	value = strings.ReplaceAll(value, "~", "~0")
	return strings.ReplaceAll(value, "/", "~1")
}

func lessUTF16(left, right string) bool {
	a := utf16CodeUnitIterator{value: left}
	b := utf16CodeUnitIterator{value: right}
	for {
		leftUnit, leftOK := a.next()
		rightUnit, rightOK := b.next()
		if !leftOK || !rightOK {
			return !leftOK && rightOK
		}
		if leftUnit != rightUnit {
			return leftUnit < rightUnit
		}
	}
}

type utf16CodeUnitIterator struct {
	value        string
	offset       int
	lowSurrogate uint16
}

func (i *utf16CodeUnitIterator) next() (uint16, bool) {
	if i.lowSurrogate != 0 {
		unit := i.lowSurrogate
		i.lowSurrogate = 0
		return unit, true
	}
	if i.offset >= len(i.value) {
		return 0, false
	}
	if i.value[i.offset] < utf8.RuneSelf {
		unit := i.value[i.offset]
		i.offset++
		return uint16(unit), true
	}
	r, size := utf8.DecodeRuneInString(i.value[i.offset:])
	i.offset += size
	if r < 0x10000 {
		return uint16(r), true
	}
	r -= 0x10000
	i.lowSurrogate = uint16(0xdc00 + (r & 0x3ff))
	return uint16(0xd800 + (r >> 10)), true
}

func isDigit(value byte) bool { return value >= '0' && value <= '9' }

func hexValue(value byte) (byte, bool) {
	switch {
	case value >= '0' && value <= '9':
		return value - '0', true
	case value >= 'a' && value <= 'f':
		return value - 'a' + 10, true
	case value >= 'A' && value <= 'F':
		return value - 'A' + 10, true
	default:
		return 0, false
	}
}
