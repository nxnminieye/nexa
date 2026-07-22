package franz

import (
	"github.com/nxnminieye/nexa/runtime/kafka"
	"github.com/twmb/franz-go/pkg/kgo"
)

func recordFromKGO(source *kgo.Record) (kafka.Record, error) {
	if source == nil {
		return kafka.Record{}, adapterError(errorKindRecordInvalid, "", "", nil)
	}
	headers := make([]kafka.Header, len(source.Headers))
	for index, header := range source.Headers {
		headers[index] = kafka.Header{Key: header.Key, Value: cloneBytes(header.Value)}
	}
	record, err := kafka.NewRecord(kafka.RecordSpec{
		Topic: source.Topic, Partition: source.Partition, Offset: source.Offset,
		Timestamp: source.Timestamp, Key: cloneBytes(source.Key), Value: cloneBytes(source.Value), Headers: headers,
	})
	if err != nil {
		return kafka.Record{}, adapterError(errorKindRecordInvalid, "", "", err)
	}
	return record, nil
}

func messageToKGO(message kafka.Message) (*kgo.Record, error) {
	if !message.Valid() {
		return nil, adapterError(errorKindMessageRequired, "/message", "", nil)
	}
	headers := message.Headers()
	convertedHeaders := make([]kgo.RecordHeader, len(headers))
	for index, header := range headers {
		convertedHeaders[index] = kgo.RecordHeader{Key: header.Key, Value: cloneBytes(header.Value)}
	}
	return &kgo.Record{
		Topic: message.Topic(), Key: cloneBytes(message.Key()), Value: cloneBytes(message.Value()), Headers: convertedHeaders,
	}, nil
}

func cloneBytes(value []byte) []byte {
	if value == nil {
		return nil
	}
	cloned := make([]byte, len(value))
	copy(cloned, value)
	return cloned
}
