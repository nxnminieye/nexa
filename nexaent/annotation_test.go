package nexaent

import (
	"bytes"
	"encoding/json"
	"fmt"
	"reflect"
	"sync"
	"testing"

	jsonschema "github.com/santhosh-tekuri/jsonschema/v6"
)

func TestAnnotationNamesAndClosedCRUDOperations(t *testing.T) {
	if got := Schema(validSchemaMeta()).Name(); got != SchemaAnnotationName {
		t.Fatalf("Schema.Name() = %q, want %q", got, SchemaAnnotationName)
	}
	if got := Field(validFieldMeta(t)).Name(); got != FieldAnnotationName {
		t.Fatalf("Field.Name() = %q, want %q", got, FieldAnnotationName)
	}
	if got := CRUD(CRUDGet).Name(); got != CRUDAnnotationName {
		t.Fatalf("CRUD.Name() = %q, want %q", got, CRUDAnnotationName)
	}

	want := []CRUDOperation{CRUDList, CRUDGet, CRUDCreate, CRUDUpdate, CRUDDelete}
	first := AllCRUDOperations()
	if !reflect.DeepEqual(first, want) {
		t.Fatalf("AllCRUDOperations() = %#v, want %#v", first, want)
	}
	first[0] = CRUDDelete
	if got := AllCRUDOperations(); !reflect.DeepEqual(got, want) {
		t.Fatalf("AllCRUDOperations() retained caller mutation: %#v", got)
	}
}

func TestCanonicalJSONUsesJCSAndReturnsFreshSlices(t *testing.T) {
	annotation := Schema(validSchemaMeta())
	want := []byte(`{"apiVersion":"nexa.dev/ent-schema-meta/v1","kind":"EntSchemaMeta","payload":{"description":{"enUS":"Account record","key":"account.description","zhCN":"Account record zh"},"identity":"ent-id","label":{"enUS":"Account","key":"account.label","zhCN":"Account zh"},"scope":"tenant"}}`)

	first, err := annotation.CanonicalJSON()
	if err != nil {
		t.Fatalf("CanonicalJSON() error = %v", err)
	}
	if !bytes.Equal(first, want) {
		t.Fatalf("CanonicalJSON() = %s, want %s", first, want)
	}
	if len(first) == 0 || first[len(first)-1] == '\n' {
		t.Fatalf("CanonicalJSON() has an unexpected trailing newline: %q", first)
	}
	original := append([]byte(nil), first...)
	first[0] = 'X'
	second, err := annotation.CanonicalJSON()
	if err != nil {
		t.Fatalf("second CanonicalJSON() error = %v", err)
	}
	if !bytes.Equal(second, original) {
		t.Fatalf("canonical mutation leaked: %s", second)
	}
	if len(first) > 0 && &first[0] == &second[0] {
		t.Fatal("CanonicalJSON() reused caller-owned storage")
	}

	const workers = 32
	results := make(chan []byte, workers)
	errors := make(chan error, workers)
	var wait sync.WaitGroup
	for range workers {
		wait.Add(1)
		go func() {
			defer wait.Done()
			encoded, encodeErr := annotation.CanonicalJSON()
			if encodeErr != nil {
				errors <- encodeErr
				return
			}
			results <- encoded
		}()
	}
	wait.Wait()
	close(results)
	close(errors)
	for encodeErr := range errors {
		t.Errorf("concurrent CanonicalJSON() error = %v", encodeErr)
	}
	for encoded := range results {
		if !bytes.Equal(encoded, original) {
			t.Errorf("concurrent CanonicalJSON() = %s", encoded)
		}
		if len(encoded) > 0 {
			encoded[0] = 'Y'
		}
	}
	third, err := annotation.CanonicalJSON()
	if err != nil || !bytes.Equal(third, original) {
		t.Fatalf("concurrent caller mutation leaked: %s, %v", third, err)
	}
}

func TestTransportEscapingDoesNotChangeSemanticBytes(t *testing.T) {
	separators := string([]rune{rune(0x2028), rune(0x2029)})
	if got := []rune(separators); !reflect.DeepEqual(got, []rune{rune(0x2028), rune(0x2029)}) {
		t.Fatalf("separator fixture contains runes %#U", got)
	}
	for _, test := range []struct {
		name string
		text string
	}{
		{name: "html-sensitive characters", text: "<>&"},
		{name: "unicode line and paragraph separators", text: "x" + separators},
		{name: "combined", text: "<>&" + separators},
	} {
		t.Run(test.name, func(t *testing.T) {
			meta := validSchemaMeta()
			meta.Label = LocalizedText{Key: "escape.label", ZhCN: test.text, EnUS: test.text}
			annotation := Schema(meta)
			transport, err := json.Marshal(annotation)
			if err != nil {
				t.Fatalf("json.Marshal() error = %v", err)
			}
			canonical, err := annotation.CanonicalJSON()
			if err != nil {
				t.Fatalf("CanonicalJSON() error = %v", err)
			}
			if bytes.Equal(transport, canonical) {
				t.Fatalf("transport bytes unexpectedly equal semantic bytes: %s", transport)
			}
			if test.name == "unicode line and paragraph separators" && !bytes.Contains(canonical, []byte(separators)) {
				t.Fatalf("canonical JSON does not contain the semantic separator runes: %x", canonical)
			}
			decoded, err := DecodeSchema(transport)
			if err != nil {
				t.Fatalf("DecodeSchema(transport) error = %v", err)
			}
			if !reflect.DeepEqual(decoded.Label, meta.Label) {
				t.Fatalf("decoded label = %#v, want %#v", decoded.Label, meta.Label)
			}
			reencoded, err := Schema(decoded).CanonicalJSON()
			if err != nil || !bytes.Equal(reencoded, canonical) {
				t.Fatalf("transport round-trip changed semantic bytes: %s, %v", reencoded, err)
			}
		})
	}
}

func TestCRUDCanonicalOrderAndDefensiveCopies(t *testing.T) {
	operations := []CRUDOperation{CRUDCreate, CRUDGet}
	annotation := CRUD(operations...)
	operations[0] = CRUDDelete

	transport, err := json.Marshal(annotation)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	decoded, err := DecodeCRUD(transport)
	if err != nil {
		t.Fatalf("DecodeCRUD() error = %v", err)
	}
	want := []CRUDOperation{CRUDGet, CRUDCreate}
	if got := decoded.Operations(); !reflect.DeepEqual(got, want) {
		t.Fatalf("Operations() = %#v, want %#v", got, want)
	}
	returned := decoded.Operations()
	returned[0] = CRUDDelete
	if got := decoded.Operations(); !reflect.DeepEqual(got, want) {
		t.Fatalf("Operations() retained caller mutation: %#v", got)
	}
	canonical, err := annotation.CanonicalJSON()
	if err != nil {
		t.Fatalf("CanonicalJSON() error = %v", err)
	}
	if wantJSON := `{"apiVersion":"nexa.dev/ent-crud/v1","kind":"EntCRUD","payload":{"operations":["get","create"]}}`; string(canonical) != wantJSON {
		t.Fatalf("CRUD canonical JSON = %s, want %s", canonical, wantJSON)
	}
}

func TestFieldConstructorAndDecoderDeepCopyPointerGraphs(t *testing.T) {
	meta := validFieldMeta(t)
	want := cloneFieldMeta(meta)
	annotation := Field(meta)

	meta.Label.Key = "mutated.label"
	meta.Description.EnUS = "mutated description"
	meta.PhysicalDisplay.Field = "mutated"
	meta.CRUD.Read = ReadExclude

	transport, err := json.Marshal(annotation)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	first, err := DecodeField(transport)
	if err != nil {
		t.Fatalf("DecodeField() error = %v", err)
	}
	second, err := DecodeField(transport)
	if err != nil {
		t.Fatalf("second DecodeField() error = %v", err)
	}
	if !reflect.DeepEqual(first, want) {
		t.Fatalf("decoded field = %#v, want %#v", first, want)
	}
	if first.PhysicalDisplay == second.PhysicalDisplay || first.CRUD == second.CRUD {
		t.Fatal("DecodeField() reused pointer values across results")
	}
	first.PhysicalDisplay.Field = "mutated"
	first.CRUD.Read = ReadExclude
	if !reflect.DeepEqual(second, want) {
		t.Fatalf("decoded result mutation leaked: %#v", second)
	}
}

func TestConcurrentMarshalDoesNotRetainCallerPointers(t *testing.T) {
	physical := &PhysicalDisplay{Field: "name"}
	policy := &CRUDFieldPolicy{Read: ReadInclude, Mutation: MutationCreateUpdate}
	meta := validFieldMeta(t)
	meta.PhysicalDisplay = physical
	meta.CRUD = policy
	annotation := Field(meta)
	want, err := annotation.CanonicalJSON()
	if err != nil {
		t.Fatalf("CanonicalJSON() error = %v", err)
	}

	const iterations = 100
	start := make(chan struct{})
	errors := make(chan error, iterations*2+1)
	var wait sync.WaitGroup
	wait.Add(1)
	go func() {
		defer wait.Done()
		<-start
		for index := range iterations {
			physical.Field = fmt.Sprintf("Mutated%d", index)
			policy.Read = ReadPolicy(fmt.Sprintf("mutated-%d", index))
		}
	}()
	for range iterations {
		wait.Add(2)
		go func() {
			defer wait.Done()
			<-start
			transport, marshalErr := json.Marshal(annotation)
			if marshalErr != nil {
				errors <- marshalErr
				return
			}
			decoded, decodeErr := DecodeField(transport)
			if decodeErr != nil || decoded.PhysicalDisplay == nil || decoded.PhysicalDisplay.Field != "name" {
				errors <- fmt.Errorf("transport changed: %#v, %v", decoded, decodeErr)
			}
		}()
		go func() {
			defer wait.Done()
			<-start
			canonical, canonicalErr := annotation.CanonicalJSON()
			if canonicalErr != nil || !bytes.Equal(canonical, want) {
				errors <- fmt.Errorf("canonical changed: %s, %v", canonical, canonicalErr)
			}
		}()
	}
	close(start)
	wait.Wait()
	close(errors)
	for concurrentErr := range errors {
		t.Error(concurrentErr)
	}
}

func TestMergeAlwaysReturnsOwnerConstantDuplicateSentinel(t *testing.T) {
	tests := []struct {
		name       string
		annotation Annotation
		other      Annotation
		invalid    Annotation
		apiVersion string
		kind       string
		decode     func([]byte) error
	}{
		{
			name: "schema", annotation: Schema(validSchemaMeta()), other: Schema(validSchemaMeta()),
			invalid: Schema(SchemaMeta{}), apiVersion: SchemaAnnotationName, kind: SchemaAnnotationKind,
			decode: func(data []byte) error { _, err := DecodeSchema(data); return err },
		},
		{
			name: "field", annotation: Field(validFieldMeta(t)), other: Field(validFieldMeta(t)),
			invalid: Field(FieldMeta{}), apiVersion: FieldAnnotationName, kind: FieldAnnotationKind,
			decode: func(data []byte) error { _, err := DecodeField(data); return err },
		},
		{
			name: "crud", annotation: CRUD(CRUDGet), other: CRUD(CRUDList), invalid: CRUD(),
			apiVersion: CRUDAnnotationName, kind: CRUDAnnotationKind,
			decode: func(data []byte) error { _, err := DecodeCRUD(data); return err },
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			first := mergedAnnotation(t, test.annotation, test.other)
			values := []Annotation{
				first,
				mergedAnnotation(t, test.annotation, nil),
				mergedAnnotation(t, test.annotation, namedOnlyAnnotation(test.apiVersion)),
				mergedAnnotation(t, test.annotation, namedOnlyAnnotation("wrong/name")),
				mergedAnnotation(t, first, test.other),
				mergedAnnotation(t, test.invalid, test.annotation),
				mergedAnnotation(t, test.other, test.annotation),
			}
			want := fmt.Sprintf(`{"apiVersion":%q,"duplicate":true,"kind":%q}`, test.apiVersion, test.kind)
			for index, value := range values {
				transport, err := json.Marshal(value)
				if err != nil {
					t.Fatalf("value %d json.Marshal() error = %v", index, err)
				}
				if string(transport) != want {
					t.Fatalf("value %d transport = %s, want %s", index, transport, want)
				}
				assertTypedError(t, test.decode(transport), "annotation_duplicate", "duplicate_annotation", "/duplicate", test.apiVersion)
				canonical, canonicalErr := value.CanonicalJSON()
				if canonical != nil {
					t.Fatalf("duplicate CanonicalJSON() bytes = %s", canonical)
				}
				assertTypedError(t, canonicalErr, "annotation_duplicate", "duplicate_annotation", "/duplicate", test.apiVersion)
			}
		})
	}
}

func TestOwnerSchemasAreEmbeddedConformanceContractsAndFreshCopies(t *testing.T) {
	tests := []struct {
		name      string
		id        string
		accessor  func() []byte
		documents []Annotation
	}{
		{
			name: "schema", id: "https://nexa.dev/schemas/nexaent/ent-schema-meta-v1.schema.json", accessor: SchemaAnnotationSchema,
			documents: []Annotation{Schema(validSchemaMeta()), mergedAnnotation(t, Schema(validSchemaMeta()), Schema(validSchemaMeta())), Schema(SchemaMeta{})},
		},
		{
			name: "field", id: "https://nexa.dev/schemas/nexaent/ent-field-meta-v1.schema.json", accessor: FieldAnnotationSchema,
			documents: []Annotation{Field(validFieldMeta(t)), mergedAnnotation(t, Field(validFieldMeta(t)), Field(validFieldMeta(t))), Field(FieldMeta{})},
		},
		{
			name: "crud", id: "https://nexa.dev/schemas/nexaent/ent-crud-v1.schema.json", accessor: CRUDAnnotationSchema,
			documents: []Annotation{CRUD(CRUDGet), mergedAnnotation(t, CRUD(CRUDGet), CRUD(CRUDList)), CRUD()},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			first := test.accessor()
			if !json.Valid(first) {
				t.Fatalf("schema accessor returned invalid JSON: %s", first)
			}
			original := append([]byte(nil), first...)
			first[0] ^= 0xff
			if got := test.accessor(); !bytes.Equal(got, original) {
				t.Fatal("schema accessor retained caller mutation")
			}
			var schemaDocument any
			if err := json.Unmarshal(original, &schemaDocument); err != nil {
				t.Fatal(err)
			}
			compiler := jsonschema.NewCompiler()
			if err := compiler.AddResource(test.id, schemaDocument); err != nil {
				t.Fatalf("AddResource() error = %v", err)
			}
			compiled, err := compiler.Compile(test.id)
			if err != nil {
				t.Fatalf("Compile() error = %v", err)
			}
			for index, annotation := range test.documents {
				transport, marshalErr := json.Marshal(annotation)
				if marshalErr != nil {
					t.Fatalf("document %d json.Marshal() error = %v", index, marshalErr)
				}
				var document any
				if unmarshalErr := json.Unmarshal(transport, &document); unmarshalErr != nil {
					t.Fatalf("document %d json.Unmarshal() error = %v", index, unmarshalErr)
				}
				if validateErr := compiled.Validate(document); validateErr != nil {
					t.Fatalf("schema rejected document %d (%s): %v", index, transport, validateErr)
				}
			}
		})
	}
}

type namedOnlyAnnotation string

func (a namedOnlyAnnotation) Name() string { return string(a) }

func mergedAnnotation(t *testing.T, receiver Annotation, other interface{ Name() string }) Annotation {
	t.Helper()
	merged := receiver.Merge(other)
	annotation, ok := merged.(Annotation)
	if !ok {
		t.Fatalf("Merge() returned %T, want nexaent.Annotation", merged)
	}
	return annotation
}

func validText(prefix string) LocalizedText {
	return LocalizedText{Key: prefix, ZhCN: prefix + " zh", EnUS: prefix + " en"}
}

func validSchemaMeta() SchemaMeta {
	return SchemaMeta{
		Label:       LocalizedText{Key: "account.label", ZhCN: "Account zh", EnUS: "Account"},
		Description: LocalizedText{Key: "account.description", ZhCN: "Account record zh", EnUS: "Account record"},
		Identity:    IdentityEntID,
		Scope:       ScopeTenant,
	}
}

func validFieldMeta(t *testing.T) FieldMeta {
	t.Helper()
	return FieldMeta{
		Label:           validText("account.name.label"),
		Description:     validText("account.name.description"),
		UIHint:          UIHintText,
		PhysicalDisplay: &PhysicalDisplay{Field: "name"},
		Visibility:      VisibilityPublic,
		CRUD:            &CRUDFieldPolicy{Read: ReadInclude, Mutation: MutationCreateUpdate},
	}
}

func cloneFieldMeta(meta FieldMeta) FieldMeta {
	cloned := meta
	if meta.PhysicalDisplay != nil {
		physical := *meta.PhysicalDisplay
		cloned.PhysicalDisplay = &physical
	}
	if meta.LogicalReference != nil {
		logical := *meta.LogicalReference
		cloned.LogicalReference = &logical
	}
	if meta.CRUD != nil {
		crud := *meta.CRUD
		cloned.CRUD = &crud
	}
	return cloned
}

func assertTypedError(t *testing.T, err error, code, reason, pointer, source string) {
	t.Helper()
	if err == nil {
		t.Fatalf("error = nil, want %s/%s %s", code, reason, pointer)
	}
	typed, ok := err.(*Error)
	if !ok {
		t.Fatalf("error type = %T, want *nexaent.Error (%v)", err, err)
	}
	if typed.Code() != code || typed.Reason() != reason || typed.Pointer() != pointer || typed.Source() != source {
		t.Fatalf("error = code=%q reason=%q pointer=%q source=%q, want code=%q reason=%q pointer=%q source=%q", typed.Code(), typed.Reason(), typed.Pointer(), typed.Source(), code, reason, pointer, source)
	}
	if typed.Error() == "" {
		t.Fatal("Error() returned an empty diagnostic")
	}
}
