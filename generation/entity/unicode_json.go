package entity

import (
	"errors"
	"strconv"
	"strings"
	"unicode/utf16"
	"unicode/utf8"
)

var errEntityRawJSONSyntax = errors.New("invalid raw JSON syntax")

type entityRawJSONUnicodeScanner struct {
	data           []byte
	pos            int
	unicodePointer string
	unicodeFound   bool
}

func entityUnicodeFailure(data []byte) (string, bool) {
	scanner := entityRawJSONUnicodeScanner{data: data}
	scanner.skipSpace()
	if scanner.pos >= len(data) || data[scanner.pos] != '{' {
		return "", false
	}
	if err := scanner.scanValue(""); err != nil {
		return "", false
	}
	scanner.skipSpace()
	if scanner.pos != len(data) || !scanner.unicodeFound {
		return "", false
	}
	return scanner.unicodePointer, true
}

func (s *entityRawJSONUnicodeScanner) scanValue(pointer string) error {
	if s.pos >= len(s.data) {
		return errEntityRawJSONSyntax
	}
	switch s.data[s.pos] {
	case '{':
		return s.scanObject(pointer)
	case '[':
		return s.scanArray(pointer)
	case '"':
		_, err := s.scanString(pointer)
		return err
	case 't':
		return s.scanLiteral("true")
	case 'f':
		return s.scanLiteral("false")
	case 'n':
		return s.scanLiteral("null")
	default:
		return s.scanNumber()
	}
}

func (s *entityRawJSONUnicodeScanner) scanObject(pointer string) error {
	s.pos++
	s.skipSpace()
	if s.consume('}') {
		return nil
	}
	seen := make(map[string]struct{})
	for {
		if s.pos >= len(s.data) || s.data[s.pos] != '"' {
			return errEntityRawJSONSyntax
		}
		key, err := s.scanString(pointer)
		if err != nil {
			return err
		}
		if _, duplicate := seen[key]; duplicate {
			return errEntityRawJSONSyntax
		}
		seen[key] = struct{}{}
		s.skipSpace()
		if !s.consume(':') {
			return errEntityRawJSONSyntax
		}
		s.skipSpace()
		if err := s.scanValue(joinEntityJSONPointer(pointer, key)); err != nil {
			return err
		}
		s.skipSpace()
		if s.consume('}') {
			return nil
		}
		if !s.consume(',') {
			return errEntityRawJSONSyntax
		}
		s.skipSpace()
	}
}

func (s *entityRawJSONUnicodeScanner) scanArray(pointer string) error {
	s.pos++
	s.skipSpace()
	if s.consume(']') {
		return nil
	}
	for index := 0; ; index++ {
		if err := s.scanValue(joinEntityJSONPointer(pointer, strconv.Itoa(index))); err != nil {
			return err
		}
		s.skipSpace()
		if s.consume(']') {
			return nil
		}
		if !s.consume(',') {
			return errEntityRawJSONSyntax
		}
		s.skipSpace()
	}
}

func (s *entityRawJSONUnicodeScanner) scanString(pointer string) (string, error) {
	if !s.consume('"') {
		return "", errEntityRawJSONSyntax
	}
	var decoded strings.Builder
	for s.pos < len(s.data) {
		current := s.data[s.pos]
		switch {
		case current == '"':
			s.pos++
			return decoded.String(), nil
		case current == '\\':
			s.pos++
			if s.pos >= len(s.data) {
				return "", errEntityRawJSONSyntax
			}
			escaped := s.data[s.pos]
			s.pos++
			switch escaped {
			case '"', '\\', '/':
				decoded.WriteByte(escaped)
			case 'b':
				decoded.WriteByte('\b')
			case 'f':
				decoded.WriteByte('\f')
			case 'n':
				decoded.WriteByte('\n')
			case 'r':
				decoded.WriteByte('\r')
			case 't':
				decoded.WriteByte('\t')
			case 'u':
				codeUnit, err := s.scanHexCodeUnit()
				if err != nil {
					return "", err
				}
				switch {
				case codeUnit >= 0xd800 && codeUnit <= 0xdbff:
					if s.pos+2 > len(s.data) || s.data[s.pos] != '\\' || s.data[s.pos+1] != 'u' {
						s.recordUnicode(pointer)
						writeEntityInvalidCodeUnit(&decoded, codeUnit)
						continue
					}
					s.pos += 2
					low, lowErr := s.scanHexCodeUnit()
					if lowErr != nil {
						return "", lowErr
					}
					if low < 0xdc00 || low > 0xdfff {
						s.recordUnicode(pointer)
						writeEntityInvalidCodeUnit(&decoded, codeUnit)
						writeEntityInvalidCodeUnit(&decoded, low)
						continue
					}
					decoded.WriteRune(utf16.DecodeRune(rune(codeUnit), rune(low)))
				case codeUnit >= 0xdc00 && codeUnit <= 0xdfff:
					s.recordUnicode(pointer)
					writeEntityInvalidCodeUnit(&decoded, codeUnit)
				default:
					decoded.WriteRune(rune(codeUnit))
				}
			default:
				return "", errEntityRawJSONSyntax
			}
		case current < 0x20:
			return "", errEntityRawJSONSyntax
		case current < utf8.RuneSelf:
			decoded.WriteByte(current)
			s.pos++
		default:
			r, size := utf8.DecodeRune(s.data[s.pos:])
			if r == utf8.RuneError && size == 1 {
				s.recordUnicode(pointer)
				decoded.WriteByte(current)
				s.pos++
				continue
			}
			decoded.WriteRune(r)
			s.pos += size
		}
	}
	return "", errEntityRawJSONSyntax
}

func (s *entityRawJSONUnicodeScanner) recordUnicode(pointer string) {
	if !s.unicodeFound {
		s.unicodePointer = pointer
		s.unicodeFound = true
	}
}

func writeEntityInvalidCodeUnit(decoded *strings.Builder, codeUnit uint16) {
	decoded.WriteByte(0xff)
	decoded.WriteByte(byte(codeUnit >> 8))
	decoded.WriteByte(byte(codeUnit))
}

func (s *entityRawJSONUnicodeScanner) scanHexCodeUnit() (uint16, error) {
	if s.pos+4 > len(s.data) {
		return 0, errEntityRawJSONSyntax
	}
	var value uint16
	for range 4 {
		value <<= 4
		switch current := s.data[s.pos]; {
		case current >= '0' && current <= '9':
			value |= uint16(current - '0')
		case current >= 'a' && current <= 'f':
			value |= uint16(current-'a') + 10
		case current >= 'A' && current <= 'F':
			value |= uint16(current-'A') + 10
		default:
			return 0, errEntityRawJSONSyntax
		}
		s.pos++
	}
	return value, nil
}

func (s *entityRawJSONUnicodeScanner) scanLiteral(literal string) error {
	if s.pos+len(literal) > len(s.data) || string(s.data[s.pos:s.pos+len(literal)]) != literal {
		return errEntityRawJSONSyntax
	}
	s.pos += len(literal)
	return nil
}

func (s *entityRawJSONUnicodeScanner) scanNumber() error {
	start := s.pos
	s.consume('-')
	if s.consume('0') {
	} else {
		if s.pos >= len(s.data) || s.data[s.pos] < '1' || s.data[s.pos] > '9' {
			return errEntityRawJSONSyntax
		}
		for s.pos < len(s.data) && s.data[s.pos] >= '0' && s.data[s.pos] <= '9' {
			s.pos++
		}
	}
	if s.consume('.') && !s.scanDigits() {
		return errEntityRawJSONSyntax
	}
	if s.pos < len(s.data) && (s.data[s.pos] == 'e' || s.data[s.pos] == 'E') {
		s.pos++
		if s.pos < len(s.data) && (s.data[s.pos] == '+' || s.data[s.pos] == '-') {
			s.pos++
		}
		if !s.scanDigits() {
			return errEntityRawJSONSyntax
		}
	}
	if s.pos == start {
		return errEntityRawJSONSyntax
	}
	return nil
}

func (s *entityRawJSONUnicodeScanner) scanDigits() bool {
	start := s.pos
	for s.pos < len(s.data) && s.data[s.pos] >= '0' && s.data[s.pos] <= '9' {
		s.pos++
	}
	return s.pos > start
}

func (s *entityRawJSONUnicodeScanner) skipSpace() {
	for s.pos < len(s.data) {
		switch s.data[s.pos] {
		case ' ', '\t', '\n', '\r':
			s.pos++
		default:
			return
		}
	}
}

func (s *entityRawJSONUnicodeScanner) consume(character byte) bool {
	if s.pos >= len(s.data) || s.data[s.pos] != character {
		return false
	}
	s.pos++
	return true
}

func joinEntityJSONPointer(base, component string) string {
	component = strings.ReplaceAll(component, "~", "~0")
	component = strings.ReplaceAll(component, "/", "~1")
	return base + "/" + component
}
