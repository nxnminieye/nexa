package api

import "unicode/utf8"

func (o requestObject) appendJSON(result []byte) []byte {
	result = append(result, '{')
	for index, member := range o {
		if index != 0 {
			result = append(result, ',')
		}
		result = appendJSONString(result, member.name)
		result = append(result, ':')
		result = member.value.appendJSON(result)
	}
	return append(result, '}')
}

func (a requestArray) appendJSON(result []byte) []byte {
	result = append(result, '[')
	for index, value := range a {
		if index != 0 {
			result = append(result, ',')
		}
		result = value.appendJSON(result)
	}
	return append(result, ']')
}

func (s requestString) appendJSON(result []byte) []byte {
	return appendJSONString(result, string(s))
}

func (n requestNumber) appendJSON(result []byte) []byte {
	return append(result, string(n)...)
}

func (b requestBool) appendJSON(result []byte) []byte {
	if b {
		return append(result, "true"...)
	}
	return append(result, "false"...)
}

func (requestNull) appendJSON(result []byte) []byte {
	return append(result, "null"...)
}

func appendJSONString(result []byte, value string) []byte {
	result = append(result, '"')
	for len(value) > 0 {
		r, size := utf8.DecodeRuneInString(value)
		value = value[size:]
		switch r {
		case '"', '\\':
			result = append(result, '\\', byte(r))
		case '\b':
			result = append(result, '\\', 'b')
		case '\f':
			result = append(result, '\\', 'f')
		case '\n':
			result = append(result, '\\', 'n')
		case '\r':
			result = append(result, '\\', 'r')
		case '\t':
			result = append(result, '\\', 't')
		default:
			if r < 0x20 {
				const hex = "0123456789abcdef"
				result = append(result, '\\', 'u', '0', '0', hex[byte(r)>>4], hex[byte(r)&0xf])
			} else {
				result = utf8.AppendRune(result, r)
			}
		}
	}
	return append(result, '"')
}
