package kafka

import "time"

// RecordSpec is the nearest caller-owned input for one consumed record.
type RecordSpec struct {
	Topic     string
	Partition int32
	Offset    int64
	Timestamp time.Time
	Key       []byte
	Value     []byte
	Headers   []Header
}

// Record is an immutable consumed Kafka record.
type Record struct {
	topic     string
	partition int32
	offset    int64
	timestamp time.Time
	key       []byte
	value     []byte
	headers   []Header
	valid     bool
}

func NewRecord(spec RecordSpec) (Record, error) {
	if !validTopic(spec.Topic) {
		return Record{}, configurationInvalid(errorKindTopicInvalid, "/topic")
	}
	if spec.Partition < 0 {
		return Record{}, configurationInvalid(errorKindPartitionInvalid, "/partition")
	}
	if spec.Offset < 0 {
		return Record{}, configurationInvalid(errorKindOffsetInvalid, "/offset")
	}
	if err := validateHeaders(spec.Headers); err != nil {
		return Record{}, err
	}
	timestamp := spec.Timestamp
	if !timestamp.IsZero() {
		timestamp = timestamp.Round(0).UTC()
	}
	return Record{
		topic:     spec.Topic,
		partition: spec.Partition,
		offset:    spec.Offset,
		timestamp: timestamp,
		key:       cloneBytes(spec.Key),
		value:     cloneBytes(spec.Value),
		headers:   cloneHeaders(spec.Headers),
		valid:     true,
	}, nil
}

func (r Record) Topic() string { return r.topic }

func (r Record) Partition() int32 { return r.partition }

func (r Record) Offset() int64 { return r.offset }

func (r Record) Timestamp() time.Time { return r.timestamp }

func (r Record) Key() []byte { return cloneBytes(r.key) }

func (r Record) Value() []byte { return cloneBytes(r.value) }

func (r Record) Headers() []Header { return cloneHeaders(r.headers) }

func (r Record) Valid() bool { return r.valid }

// MessageSpec is the nearest caller-owned input for one produced message.
type MessageSpec struct {
	Topic   string
	Key     []byte
	Value   []byte
	Headers []Header
}

// Message is an immutable Kafka producer message.
type Message struct {
	topic   string
	key     []byte
	value   []byte
	headers []Header
	valid   bool
}

func NewMessage(spec MessageSpec) (Message, error) {
	if !validTopic(spec.Topic) {
		return Message{}, configurationInvalid(errorKindTopicInvalid, "/topic")
	}
	if err := validateHeaders(spec.Headers); err != nil {
		return Message{}, err
	}
	return Message{
		topic:   spec.Topic,
		key:     cloneBytes(spec.Key),
		value:   cloneBytes(spec.Value),
		headers: cloneHeaders(spec.Headers),
		valid:   true,
	}, nil
}

func (m Message) Topic() string { return m.topic }

func (m Message) Key() []byte { return cloneBytes(m.key) }

func (m Message) Value() []byte { return cloneBytes(m.value) }

func (m Message) Headers() []Header { return cloneHeaders(m.headers) }

func (m Message) Valid() bool { return m.valid }

// Batch is an immutable ordered snapshot returned by a Reader.
type Batch struct {
	records []Record
	valid   bool
}

func NewBatch(records []Record) (Batch, error) {
	if len(records) == 0 {
		return Batch{}, configurationInvalid(errorKindBatchEmpty, "/records")
	}
	for index, record := range records {
		if !record.Valid() {
			return Batch{}, configurationInvalid(errorKindBatchRecordInvalid, recordPointer(index))
		}
	}
	return Batch{records: cloneRecords(records), valid: true}, nil
}

func (b Batch) Len() int { return len(b.records) }

func (b Batch) Records() []Record { return cloneRecords(b.records) }

func (b Batch) Valid() bool { return b.valid }

func cloneRecord(record Record) Record {
	if !record.valid {
		return Record{}
	}
	record.key = cloneBytes(record.key)
	record.value = cloneBytes(record.value)
	record.headers = cloneHeaders(record.headers)
	return record
}

func cloneRecords(records []Record) []Record {
	if records == nil {
		return nil
	}
	cloned := make([]Record, len(records))
	for index, record := range records {
		cloned[index] = cloneRecord(record)
	}
	return cloned
}
