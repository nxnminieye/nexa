package franz

import (
	"errors"
	"testing"
	"time"

	"github.com/nxnminieye/nexa/runtime/kafka"
	"github.com/twmb/franz-go/pkg/kgo"
)

func TestConvertRecordPreservesFieldsAndOwnership(t *testing.T) {
	timestamp := time.Date(2026, 7, 14, 9, 8, 7, 6, time.FixedZone("test", 8*60*60))
	source := &kgo.Record{
		Topic: "orders", Partition: 3, Offset: 42, Timestamp: timestamp,
		Key: []byte("key"), Value: []byte("value"),
		Headers: []kgo.RecordHeader{
			{Key: "trace", Value: nil},
			{Key: "trace", Value: []byte{}},
			{Key: "trace", Value: []byte("second")},
		},
	}

	record, err := recordFromKGO(source)
	if err != nil {
		t.Fatalf("recordFromKGO() error = %v", err)
	}
	if record.Topic() != "orders" || record.Partition() != 3 || record.Offset() != 42 {
		t.Fatalf("record identity = %q/%d/%d", record.Topic(), record.Partition(), record.Offset())
	}
	if !record.Timestamp().Equal(timestamp) || record.Timestamp().Location() != time.UTC {
		t.Fatalf("record timestamp = %v", record.Timestamp())
	}
	assertBytes(t, "key", record.Key(), []byte("key"))
	assertBytes(t, "value", record.Value(), []byte("value"))
	headers := record.Headers()
	if len(headers) != 3 || headers[0].Key != "trace" || headers[1].Key != "trace" || headers[2].Key != "trace" {
		t.Fatalf("headers = %#v", headers)
	}
	if headers[0].Value != nil || headers[1].Value == nil || len(headers[1].Value) != 0 {
		t.Fatalf("empty header ownership = %#v", headers)
	}
	assertBytes(t, "header", headers[2].Value, []byte("second"))

	source.Key[0] = 'X'
	source.Value[0] = 'X'
	source.Headers[2].Value[0] = 'X'
	headers[2].Value[0] = 'Y'
	assertBytes(t, "owned key", record.Key(), []byte("key"))
	assertBytes(t, "owned value", record.Value(), []byte("value"))
	assertBytes(t, "owned header", record.Headers()[2].Value, []byte("second"))
}

func TestConvertMessagePreservesOrderedHeadersAndOwnership(t *testing.T) {
	message, err := kafka.NewMessage(kafka.MessageSpec{
		Topic: "events", Key: []byte("key"), Value: []byte("value"),
		Headers: []kafka.Header{
			{Key: "trace", Value: nil},
			{Key: "trace", Value: []byte{}},
			{Key: "trace", Value: []byte("second")},
		},
	})
	if err != nil {
		t.Fatalf("kafka.NewMessage() error = %v", err)
	}

	record, err := messageToKGO(message)
	if err != nil {
		t.Fatalf("messageToKGO() error = %v", err)
	}
	if record.Topic != "events" || len(record.Headers) != 3 {
		t.Fatalf("record = %#v", record)
	}
	if record.Headers[0].Value != nil || record.Headers[1].Value == nil || len(record.Headers[1].Value) != 0 {
		t.Fatalf("empty headers = %#v", record.Headers)
	}
	assertBytes(t, "key", record.Key, []byte("key"))
	assertBytes(t, "value", record.Value, []byte("value"))
	assertBytes(t, "header", record.Headers[2].Value, []byte("second"))

	record.Key[0] = 'X'
	record.Value[0] = 'X'
	record.Headers[2].Value[0] = 'X'
	assertBytes(t, "message key", message.Key(), []byte("key"))
	assertBytes(t, "message value", message.Value(), []byte("value"))
	assertBytes(t, "message header", message.Headers()[2].Value, []byte("second"))
}

func TestConvertRejectsInvalidRecordAndMessageSafely(t *testing.T) {
	_, err := recordFromKGO(nil)
	assertFranzError(t, err, ErrFetchFailed, "franz_fetch_failed", "record_invalid", "", kafka.StagePoll)

	_, err = messageToKGO(kafka.Message{})
	assertFranzError(t, err, ErrConfigurationInvalid, "franz_configuration_invalid", "message_required", "/message", "")

	secret := errors.New("broker sasl secret")
	err = adapterError(errorKindDeliveryFailed, "", "", secret)
	if errors.Is(err, secret) || err.Error() == secret.Error() {
		t.Fatalf("delivery error exposed private cause: %v", err)
	}
}

func assertBytes(t *testing.T, name string, got, want []byte) {
	t.Helper()
	if string(got) != string(want) || (got == nil) != (want == nil) {
		t.Fatalf("%s = %#v, want %#v", name, got, want)
	}
}

func assertFranzError(t *testing.T, err error, sentinel error, code, reason, pointer string, stage kafka.Stage) {
	t.Helper()
	var typed *Error
	if !errors.As(err, &typed) {
		t.Fatalf("error = %T, want *Error", err)
	}
	if !errors.Is(err, sentinel) || typed.Code() != code || typed.Reason() != reason || typed.Pointer() != pointer {
		t.Fatalf("error projection = sentinel:%v code:%q reason:%q pointer:%q", errors.Is(err, sentinel), typed.Code(), typed.Reason(), typed.Pointer())
	}
	gotStage, ok := typed.Stage()
	if (stage != "") != ok || gotStage != stage {
		t.Fatalf("stage = %q/%v, want %q", gotStage, ok, stage)
	}
}
