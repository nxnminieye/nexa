package source

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestCommandSchemasAreClosedAndCommandSpecific(t *testing.T) {
	wantRequired := map[string][]string{
		"plan":        {"manifest-digest", "profile", "provider", "repo-root", "target", "tree-digest", "version"},
		"check":       {"manifest-digest", "profile", "provider", "repo-root", "target", "tree-digest", "version"},
		"materialize": {"manifest-digest", "profile", "provider", "repo-root", "target", "tree-digest", "version"},
		"upgrade":     {"manifest-digest", "profile", "provider", "repo-root", "target", "tree-digest", "version"},
		"status":      {"provider", "repo-root", "target"},
		"diff":        {"provider", "repo-root", "target"},
		"detach":      {"provider", "repo-root", "target"},
	}
	for command, required := range wantRequired {
		input, output := commandSchemas(command)
		var inputDocument, outputDocument struct {
			Type                 string                     `json:"type"`
			AdditionalProperties bool                       `json:"additionalProperties"`
			Required             []string                   `json:"required"`
			Properties           map[string]json.RawMessage `json:"properties"`
		}
		if err := json.Unmarshal(input, &inputDocument); err != nil || inputDocument.Type != "object" || inputDocument.AdditionalProperties {
			t.Fatalf("%s input schema = %s err=%v", command, input, err)
		}
		if err := json.Unmarshal(output, &outputDocument); err != nil || outputDocument.Type != "object" || outputDocument.AdditionalProperties {
			t.Fatalf("%s output schema = %s err=%v", command, output, err)
		}
		if !reflect.DeepEqual(inputDocument.Required, required) || len(outputDocument.Required) == 0 {
			t.Fatalf("%s required input=%v output=%v", command, inputDocument.Required, outputDocument.Required)
		}
		if (command == "materialize" || command == "upgrade") && inputDocument.Properties["expected-plan-digest"] == nil {
			t.Fatalf("%s lacks optional expected plan digest", command)
		}
		if command != "materialize" && command != "upgrade" && inputDocument.Properties["expected-plan-digest"] != nil {
			t.Fatalf("%s accepts write-only expected plan digest", command)
		}
	}
	if input, output := commandSchemas("unknown"); input != nil || output != nil {
		t.Fatalf("unknown schemas = %s %s", input, output)
	}
}
