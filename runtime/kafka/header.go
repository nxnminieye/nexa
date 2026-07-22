// Package kafka defines transport-neutral Kafka runtime contracts.
package kafka

// Header is one ordered Kafka header. Duplicate keys are valid.
type Header struct {
	Key   string
	Value []byte
}

func cloneBytes(value []byte) []byte {
	if value == nil {
		return nil
	}
	cloned := make([]byte, len(value))
	copy(cloned, value)
	return cloned
}

func cloneHeaders(headers []Header) []Header {
	if headers == nil {
		return nil
	}
	cloned := make([]Header, len(headers))
	for index, header := range headers {
		cloned[index] = Header{Key: header.Key, Value: cloneBytes(header.Value)}
	}
	return cloned
}
