package kafka

import (
	"bytes"
	"context"
	"errors"
	"math"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"
)

func TestRecordImmutableGraphAndTimestamp(t *testing.T) {
	now := time.Now()
	key := []byte("record-key")
	value := []byte("record-value")
	headers := []Header{
		{Key: "x-duplicate", Value: nil},
		{Key: "x-duplicate", Value: []byte{}},
		{Key: "x-value", Value: []byte("header-value")},
	}
	record, err := NewRecord(RecordSpec{
		Topic: "sample.topic", Partition: 2, Offset: 9, Timestamp: now,
		Key: key, Value: value, Headers: headers,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !record.Valid() || (Record{}).Valid() {
		t.Fatalf("validity = constructed %t zero %t", record.Valid(), (Record{}).Valid())
	}
	if record.Topic() != "sample.topic" || record.Partition() != 2 || record.Offset() != 9 {
		t.Fatalf("record identity = (%q,%d,%d)", record.Topic(), record.Partition(), record.Offset())
	}
	wantTimestamp := now.Round(0).UTC()
	if record.Timestamp() != wantTimestamp || record.Timestamp().Location() != time.UTC || !record.Timestamp().Equal(now) {
		t.Fatalf("timestamp = %#v, want %#v", record.Timestamp(), wantTimestamp)
	}

	key[0] = 'X'
	value[0] = 'X'
	headers[2].Key = "changed"
	headers[2].Value[0] = 'X'
	if string(record.Key()) != "record-key" || string(record.Value()) != "record-value" || record.Headers()[2].Key != "x-value" || string(record.Headers()[2].Value) != "header-value" {
		t.Fatal("constructor input mutation changed record")
	}

	readKey := record.Key()
	readValue := record.Value()
	readHeaders := record.Headers()
	readKey[0] = 'Y'
	readValue[0] = 'Y'
	readHeaders[0].Key = "changed"
	readHeaders[2].Value[0] = 'Y'
	if string(record.Key()) != "record-key" || string(record.Value()) != "record-value" || record.Headers()[0].Key != "x-duplicate" || string(record.Headers()[2].Value) != "header-value" {
		t.Fatal("accessor result mutation changed record")
	}
	if record.Headers()[0].Value != nil || record.Headers()[1].Value == nil || len(record.Headers()[1].Value) != 0 {
		t.Fatal("header nil versus empty value was not preserved")
	}
	reversed, err := NewRecord(RecordSpec{
		Topic: "sample.topic",
		Headers: []Header{
			{Key: "x-value", Value: []byte("header-value")},
			{Key: "x-duplicate", Value: []byte{}},
			{Key: "x-duplicate", Value: nil},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if string(record.Headers()[0].Value) == string(reversed.Headers()[0].Value) || record.Headers()[0].Key == reversed.Headers()[0].Key {
		t.Fatal("reversing ordered duplicate headers was not observable")
	}
}

func TestRecordNilAndEmptyBytesRemainDistinct(t *testing.T) {
	vectors := []struct {
		name     string
		key      []byte
		value    []byte
		keyNil   bool
		valueNil bool
	}{
		{name: "nil", key: nil, value: nil, keyNil: true, valueNil: true},
		{name: "empty", key: []byte{}, value: []byte{}, keyNil: false, valueNil: false},
	}
	for _, vector := range vectors {
		t.Run(vector.name, func(t *testing.T) {
			record, err := NewRecord(RecordSpec{Topic: "sample", Key: vector.key, Value: vector.value})
			if err != nil {
				t.Fatal(err)
			}
			if (record.Key() == nil) != vector.keyNil || (record.Value() == nil) != vector.valueNil {
				t.Fatalf("record bytes nil = (%t,%t)", record.Key() == nil, record.Value() == nil)
			}
			message, err := NewMessage(MessageSpec{Topic: "sample", Key: vector.key, Value: vector.value})
			if err != nil {
				t.Fatal(err)
			}
			if (message.Key() == nil) != vector.keyNil || (message.Value() == nil) != vector.valueNil {
				t.Fatalf("message bytes nil = (%t,%t)", message.Key() == nil, message.Value() == nil)
			}
		})
	}
}

func TestRecordZeroTimestampIsPreserved(t *testing.T) {
	record, err := NewRecord(RecordSpec{Topic: "sample"})
	if err != nil {
		t.Fatal(err)
	}
	if record.Timestamp() != (time.Time{}) || !record.Timestamp().IsZero() {
		t.Fatalf("zero timestamp = %#v", record.Timestamp())
	}
}

func TestRecordNumericBoundsAreInclusive(t *testing.T) {
	record, err := NewRecord(RecordSpec{Topic: "sample", Partition: math.MaxInt32, Offset: math.MaxInt64})
	if err != nil {
		t.Fatal(err)
	}
	if record.Partition() != math.MaxInt32 || record.Offset() != math.MaxInt64 {
		t.Fatalf("numeric bounds = (%d,%d)", record.Partition(), record.Offset())
	}
}

func TestRecordValidationOrderAndPointers(t *testing.T) {
	vectors := []struct {
		name    string
		spec    RecordSpec
		reason  string
		pointer string
	}{
		{name: "topic first", spec: RecordSpec{Partition: -1, Offset: -1, Headers: []Header{{}}}, reason: "topic_invalid", pointer: "/topic"},
		{name: "partition before offset", spec: RecordSpec{Topic: "valid", Partition: -1, Offset: -1, Headers: []Header{{}}}, reason: "partition_invalid", pointer: "/partition"},
		{name: "offset before header", spec: RecordSpec{Topic: "valid", Offset: -1, Headers: []Header{{}}}, reason: "offset_invalid", pointer: "/offset"},
		{name: "header by index", spec: RecordSpec{Topic: "valid", Headers: []Header{{Key: "ok"}, {Key: ""}}}, reason: "header_key_invalid", pointer: "/headers/1/key"},
	}
	for _, vector := range vectors {
		t.Run(vector.name, func(t *testing.T) {
			record, err := NewRecord(vector.spec)
			if record.Valid() {
				t.Fatal("failed constructor returned valid record")
			}
			requireConfigurationError(t, err, vector.reason, vector.pointer)
		})
	}
}

func TestMessageImmutableGraphAndValidation(t *testing.T) {
	key := []byte("key")
	value := []byte("value")
	headers := []Header{{Key: "x-a", Value: []byte("a")}, {Key: "x-a", Value: []byte("b")}}
	message, err := NewMessage(MessageSpec{Topic: "sample.topic", Key: key, Value: value, Headers: headers})
	if err != nil {
		t.Fatal(err)
	}
	if !message.Valid() || (Message{}).Valid() || message.Topic() != "sample.topic" {
		t.Fatalf("message validity/topic = %t/%t/%q", message.Valid(), (Message{}).Valid(), message.Topic())
	}
	key[0], value[0], headers[0].Value[0] = 'X', 'X', 'X'
	if string(message.Key()) != "key" || string(message.Value()) != "value" || string(message.Headers()[0].Value) != "a" {
		t.Fatal("constructor input mutation changed message")
	}
	readKey, readValue, readHeaders := message.Key(), message.Value(), message.Headers()
	readKey[0], readValue[0], readHeaders[0].Value[0] = 'Y', 'Y', 'Y'
	readHeaders[0].Key = "changed"
	if string(message.Key()) != "key" || string(message.Value()) != "value" || message.Headers()[0].Key != "x-a" || string(message.Headers()[0].Value) != "a" {
		t.Fatal("accessor result mutation changed message")
	}

	zero, err := NewMessage(MessageSpec{Topic: "", Headers: []Header{{}}})
	if zero.Valid() {
		t.Fatal("failed constructor returned valid message")
	}
	requireConfigurationError(t, err, "topic_invalid", "/topic")
	zero, err = NewMessage(MessageSpec{Topic: "valid", Headers: []Header{{Key: "ok"}, {}}})
	if zero.Valid() {
		t.Fatal("failed constructor returned valid message")
	}
	requireConfigurationError(t, err, "header_key_invalid", "/headers/1/key")
}

func TestHeaderAndTopicBoundaries(t *testing.T) {
	validUTF8Topic := strings.Repeat("é", 124) + "a"
	if len(validUTF8Topic) != 249 || !utf8.ValidString(validUTF8Topic) {
		t.Fatal("invalid topic fixture")
	}
	validTopics := []string{"a", strings.Repeat("a", 249), validUTF8Topic, "orders.v1-production"}
	for _, topic := range validTopics {
		if _, err := NewMessage(MessageSpec{Topic: topic}); err != nil {
			t.Fatalf("valid topic length %d rejected: %v", len(topic), err)
		}
	}
	invalidTopics := []string{"", ".", "..", strings.Repeat("a", 250), strings.Repeat("é", 125), "has\ncontrol", "has\u0085control", string([]byte{0xff})}
	for _, topic := range invalidTopics {
		_, err := NewMessage(MessageSpec{Topic: topic})
		requireConfigurationError(t, err, "topic_invalid", "/topic")
		if err != nil && strings.Contains(err.Error(), topic) && topic != "" {
			t.Fatalf("topic leaked in diagnostic: %q", err.Error())
		}
	}

	validKeys := []string{"x", strings.Repeat("x", 256), strings.Repeat("é", 128)}
	for _, key := range validKeys {
		if _, err := NewMessage(MessageSpec{Topic: "valid", Headers: []Header{{Key: key}}}); err != nil {
			t.Fatalf("valid header key length %d rejected: %v", len(key), err)
		}
	}
	invalidKeys := []string{"", strings.Repeat("x", 257), strings.Repeat("é", 129), "has\tcontrol", "has\u0085control", string([]byte{0xff})}
	for _, key := range invalidKeys {
		_, err := NewMessage(MessageSpec{Topic: "valid", Headers: []Header{{Key: key}}})
		requireConfigurationError(t, err, "header_key_invalid", "/headers/0/key")
	}
}

func TestTopicLengthPropertyUsesUTF8Bytes(t *testing.T) {
	for length := 1; length <= 249; length++ {
		topic := strings.Repeat("a", length)
		message, err := NewMessage(MessageSpec{Topic: topic})
		if err != nil || !message.Valid() || message.Topic() != topic {
			t.Fatalf("valid topic byte length %d rejected: %v", length, err)
		}
	}
	for _, topic := range []string{
		strings.Repeat("😀", 62) + "a",
		strings.Repeat("界", 83),
	} {
		if len(topic) != 249 {
			t.Fatalf("fixture byte length = %d", len(topic))
		}
		if _, err := NewMessage(MessageSpec{Topic: topic}); err != nil {
			t.Fatalf("valid multi-byte topic rejected: %v", err)
		}
	}
	for _, topic := range []string{strings.Repeat("a", 250), strings.Repeat("😀", 63), strings.Repeat("界", 84)} {
		_, err := NewMessage(MessageSpec{Topic: topic})
		requireConfigurationError(t, err, "topic_invalid", "/topic")
	}
}

func TestRecordAndMessageDoNotImposePayloadSizePolicy(t *testing.T) {
	key := bytes.Repeat([]byte{'k'}, 2<<20)
	value := bytes.Repeat([]byte{'v'}, 2<<20)
	headerValue := bytes.Repeat([]byte{'h'}, 2<<20)
	record, err := NewRecord(RecordSpec{
		Topic: "sample", Key: key, Value: value,
		Headers: []Header{{Key: "x-large", Value: headerValue}},
	})
	if err != nil {
		t.Fatal(err)
	}
	message, err := NewMessage(MessageSpec{
		Topic: "sample", Key: key, Value: value,
		Headers: []Header{{Key: "x-large", Value: headerValue}},
	})
	if err != nil {
		t.Fatal(err)
	}
	key[0], value[0], headerValue[0] = 'X', 'X', 'X'
	if len(record.Key()) != 2<<20 || len(record.Value()) != 2<<20 || len(record.Headers()[0].Value) != 2<<20 ||
		len(message.Key()) != 2<<20 || len(message.Value()) != 2<<20 || len(message.Headers()[0].Value) != 2<<20 {
		t.Fatal("large payload graph was rejected or truncated")
	}
	if record.Key()[0] != 'k' || record.Value()[0] != 'v' || record.Headers()[0].Value[0] != 'h' ||
		message.Key()[0] != 'k' || message.Value()[0] != 'v' || message.Headers()[0].Value[0] != 'h' {
		t.Fatal("large payload graph was not defensively copied")
	}
}

func TestBatchOrderedImmutableSnapshot(t *testing.T) {
	first, err := NewRecord(RecordSpec{Topic: "first", Key: []byte("a")})
	if err != nil {
		t.Fatal(err)
	}
	second, err := NewRecord(RecordSpec{Topic: "second", Key: []byte("b")})
	if err != nil {
		t.Fatal(err)
	}
	input := []Record{first, second}
	batch, err := NewBatch(input)
	if err != nil {
		t.Fatal(err)
	}
	if !batch.Valid() || (Batch{}).Valid() || batch.Len() != 2 {
		t.Fatalf("batch validity/length = %t/%t/%d", batch.Valid(), (Batch{}).Valid(), batch.Len())
	}
	input[0] = second
	if got := batch.Records(); got[0].Topic() != "first" || got[1].Topic() != "second" {
		t.Fatalf("batch order = %q,%q", got[0].Topic(), got[1].Topic())
	}
	read := batch.Records()
	read[0] = second
	read[1] = first
	read[0].Key()[0] = 'X'
	if got := batch.Records(); got[0].Topic() != "first" || got[1].Topic() != "second" || string(got[0].Key()) != "a" {
		t.Fatal("accessor mutation changed batch")
	}
}

func TestBatchRejectsEmptyAndInvalidMembers(t *testing.T) {
	for _, records := range [][]Record{nil, {}} {
		batch, err := NewBatch(records)
		if batch.Valid() || batch.Len() != 0 || batch.Records() != nil {
			t.Fatalf("failed batch = %#v", batch)
		}
		requireConfigurationError(t, err, "batch_empty", "/records")
	}
	valid, err := NewRecord(RecordSpec{Topic: "valid"})
	if err != nil {
		t.Fatal(err)
	}
	batch, err := NewBatch([]Record{valid, {}})
	if batch.Valid() {
		t.Fatal("batch with zero record is valid")
	}
	kafkaError := requireConfigurationError(t, err, "batch_record_invalid", "/records/1")
	if kafkaError.Reason() == "record_required" {
		t.Fatal("zero record acquired an unowned reason")
	}
}

func TestRecordConcurrentReadsAreStable(t *testing.T) {
	record, err := NewRecord(RecordSpec{
		Topic: "sample", Key: []byte("key"), Value: []byte("value"),
		Headers: []Header{{Key: "x", Value: []byte("header")}},
	})
	if err != nil {
		t.Fatal(err)
	}
	var wait sync.WaitGroup
	for range 100 {
		wait.Add(1)
		go func() {
			defer wait.Done()
			if !record.Valid() || string(record.Key()) != "key" || string(record.Value()) != "value" || string(record.Headers()[0].Value) != "header" {
				t.Error("concurrent read changed record")
			}
		}()
	}
	wait.Wait()
}

func TestConfigurationErrorsMatchNoUnrelatedSentinel(t *testing.T) {
	_, err := NewRecord(RecordSpec{})
	for _, sentinel := range []error{ErrLifecycleConflict, ErrReaderFailed, ErrHandlerFailed, ErrCommitFailed, ErrCloseFailed, ErrRetryPolicyFailed, ErrOperationCanceled, context.Canceled, context.DeadlineExceeded} {
		if errors.Is(err, sentinel) {
			t.Fatalf("configuration error matched %v", sentinel)
		}
	}
}
