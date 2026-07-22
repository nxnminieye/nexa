package api

import (
	"encoding/json"
	"errors"
	"math"
	"strconv"

	"github.com/gowebpki/jcs"
)

var errRuntimeContractCanonicalValue = errors.New("runtime contract canonical value is invalid")

func (p *requestParser) parseAnyValue(pointer string, depth int) (any, error) {
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
		return p.parseAnyObject(pointer, depth)
	case '[':
		return p.parseAnyArray(pointer, depth)
	case '"':
		return p.parseString(pointer)
	case 't':
		if p.consumeLiteral("true") {
			return true, nil
		}
	case 'f':
		if p.consumeLiteral("false") {
			return false, nil
		}
	case 'n':
		if p.consumeLiteral("null") {
			if p.allowNull {
				return nil, nil
			}
			return nil, p.failure("null_not_allowed", pointer)
		}
	default:
		if p.data[p.offset] == '-' || isDigit(p.data[p.offset]) {
			value, ok := p.parseNumber()
			if ok {
				return json.Number(value), nil
			}
		}
	}
	return nil, p.failure("invalid_json", pointer)
}

func (p *requestParser) parseAnyObject(pointer string, depth int) (map[string]any, error) {
	p.offset++
	p.skipWhitespace()
	result := make(map[string]any)
	if p.consumeByte('}') {
		return result, nil
	}
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
		if _, duplicate := result[name]; duplicate {
			return nil, p.failure("duplicate_key", childPointer)
		}
		p.skipWhitespace()
		if !p.consumeByte(':') {
			return nil, p.failure("invalid_json", childPointer)
		}
		value, err := p.parseAnyValue(childPointer, depth+1)
		if err != nil {
			return nil, err
		}
		result[name] = value
		p.skipWhitespace()
		if p.consumeByte('}') {
			return result, nil
		}
		if !p.consumeByte(',') {
			return nil, p.failure("invalid_json", pointer)
		}
	}
}

func (p *requestParser) parseAnyArray(pointer string, depth int) ([]any, error) {
	p.offset++
	p.skipWhitespace()
	result := make([]any, 0)
	if p.consumeByte(']') {
		return result, nil
	}
	for index := 0; ; index++ {
		childPointer := pointer + "/" + strconv.Itoa(index)
		value, err := p.parseAnyValue(childPointer, depth+1)
		if err != nil {
			return nil, err
		}
		result = append(result, value)
		p.skipWhitespace()
		if p.consumeByte(']') {
			return result, nil
		}
		if !p.consumeByte(',') {
			return nil, p.failure("invalid_json", pointer)
		}
	}
}

type runtimeCanonicalComparator struct {
	input  []byte
	offset int
	equal  bool
}

func canonicalRuntimeContractInput(input []byte, value any) (bool, error) {
	comparator := runtimeCanonicalComparator{input: input, equal: true}
	if err := writeCanonicalValue(&comparator, value); err != nil {
		return false, err
	}
	return comparator.equal && comparator.offset == len(input), nil
}

func (c *runtimeCanonicalComparator) writeByte(value byte) bool {
	if !c.equal {
		return false
	}
	if c.offset >= len(c.input) || c.input[c.offset] != value {
		c.equal = false
		return false
	}
	c.offset++
	return true
}

func (c *runtimeCanonicalComparator) writeString(value string) bool {
	if !c.equal {
		return false
	}
	if len(value) > len(c.input)-c.offset {
		c.equal = false
		return false
	}
	for index := 0; index < len(value); index++ {
		if c.input[c.offset+index] != value[index] {
			c.equal = false
			return false
		}
	}
	c.offset += len(value)
	return true
}

func writeCanonicalValue(sink runtimeCanonicalSink, value any) error {
	switch value := value.(type) {
	case map[string]any:
		if !sink.writeByte('{') {
			return nil
		}
		for index, key := range sortedRuntimeMapKeys(value) {
			if index != 0 && !sink.writeByte(',') {
				return nil
			}
			if !writeCanonicalJSONString(sink, key) || !sink.writeByte(':') {
				return nil
			}
			if err := writeCanonicalValue(sink, value[key]); err != nil {
				return err
			}
		}
		sink.writeByte('}')
		return nil
	case []any:
		if !sink.writeByte('[') {
			return nil
		}
		for index, item := range value {
			if index != 0 && !sink.writeByte(',') {
				return nil
			}
			if err := writeCanonicalValue(sink, item); err != nil {
				return err
			}
		}
		sink.writeByte(']')
		return nil
	case string:
		writeCanonicalJSONString(sink, value)
		return nil
	case json.Number:
		binary64, err := strconv.ParseFloat(string(value), 64)
		if err != nil || math.IsInf(binary64, 0) || math.IsNaN(binary64) {
			return errRuntimeContractCanonicalValue
		}
		canonical, err := jcs.NumberToJSON(binary64)
		if err != nil {
			return errRuntimeContractCanonicalValue
		}
		sink.writeString(canonical)
		return nil
	case bool:
		if value {
			sink.writeString("true")
		} else {
			sink.writeString("false")
		}
		return nil
	case nil:
		sink.writeString("null")
		return nil
	default:
		return errRuntimeContractCanonicalValue
	}
}
