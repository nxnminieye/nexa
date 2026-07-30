package sourcecomment

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
)

type schemaFactsWire struct {
	Label       localizedTextWire `json:"label"`
	Description localizedTextWire `json:"description"`
	Scope       RecordScope       `json:"scope"`
}

type fieldFactsWire struct {
	Label       localizedTextWire  `json:"label"`
	Description localizedTextWire  `json:"description"`
	Control     UIControl          `json:"control"`
	Reference   *ResolvedReference `json:"reference,omitempty"`
	Visibility  FieldVisibility    `json:"visibility"`
	CRUD        *CRUDFieldPolicy   `json:"crud,omitempty"`
}

type localizedTextWire struct {
	Key  string `json:"key"`
	ZhCN string `json:"zhCN"`
	EnUS string `json:"enUS"`
}

type crudOperationsWire struct {
	Operations []CRUDOperation `json:"operations"`
}

func CanonicalSchemaFacts(value SchemaFacts) ([]byte, error) {
	if err := ValidateSchemaFacts(value); err != nil {
		return nil, err
	}
	return json.Marshal(schemaFactsWire{Label: localizedWire(value.Label), Description: localizedWire(value.Description), Scope: value.Scope})
}

func CanonicalFieldFacts(value FieldFacts) ([]byte, error) {
	if err := ValidateFieldFacts(value); err != nil {
		return nil, err
	}
	wire := fieldFactsWire{Label: localizedWire(value.Label), Description: localizedWire(value.Description), Control: value.Control, Visibility: value.Visibility, CRUD: cloneCRUDPolicy(value.CRUD)}
	if value.Reference != nil {
		reference := *value.Reference
		wire.Reference = &reference
	}
	return json.Marshal(wire)
}

func CanonicalCRUDOperations(value CRUDOperations) ([]byte, error) {
	if err := ValidateCRUDOperations(value); err != nil {
		return nil, err
	}
	return json.Marshal(crudOperationsWire{Operations: value.Operations()})
}

func ParseSchemaFacts(data []byte) (SchemaFacts, error) {
	var wire schemaFactsWire
	if err := decodeStrict(data, &wire); err != nil {
		return SchemaFacts{}, err
	}
	value := SchemaFacts{Label: localizedValue(wire.Label), Description: localizedValue(wire.Description), Scope: wire.Scope}
	if err := ValidateSchemaFacts(value); err != nil {
		return SchemaFacts{}, err
	}
	return value, nil
}

func ParseFieldFacts(data []byte) (FieldFacts, error) {
	var wire fieldFactsWire
	if err := decodeStrict(data, &wire); err != nil {
		return FieldFacts{}, err
	}
	value := FieldFacts{Label: localizedValue(wire.Label), Description: localizedValue(wire.Description), Control: wire.Control, Visibility: wire.Visibility, CRUD: cloneCRUDPolicy(wire.CRUD)}
	if wire.Reference != nil {
		reference := *wire.Reference
		value.Reference = &reference
	}
	if err := ValidateFieldFacts(value); err != nil {
		return FieldFacts{}, err
	}
	return value, nil
}

func ParseCRUDOperations(data []byte) (CRUDOperations, error) {
	var wire crudOperationsWire
	if err := decodeStrict(data, &wire); err != nil {
		return CRUDOperations{}, err
	}
	return NewCRUDOperations(wire.Operations...)
}

func decodeStrict(data []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return fmt.Errorf("decode Source Comment facts: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return fmt.Errorf("decode Source Comment facts: trailing JSON")
	}
	return nil
}

func localizedWire(value LocalizedText) localizedTextWire {
	return localizedTextWire{Key: value.Key, ZhCN: value.ZhCN, EnUS: value.EnUS}
}

func localizedValue(value localizedTextWire) LocalizedText {
	return LocalizedText{Key: value.Key, ZhCN: value.ZhCN, EnUS: value.EnUS}
}

func cloneCRUDPolicy(value *CRUDFieldPolicy) *CRUDFieldPolicy {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}
