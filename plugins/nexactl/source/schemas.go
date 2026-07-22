package source

import "encoding/json"

type sourceReleaseInspection struct {
	ProviderID     string   `json:"providerId"`
	ModulePath     string   `json:"modulePath"`
	PackagePath    string   `json:"packagePath"`
	Version        string   `json:"version"`
	ManifestDigest string   `json:"manifestDigest"`
	TreeDigest     string   `json:"treeDigest"`
	Profiles       []string `json:"profiles"`
}

func (release sourceReleaseInspection) key() string {
	return release.ProviderID + "\x00" + release.ModulePath + "\x00" + release.PackagePath + "\x00" + release.Version + "\x00" +
		release.ManifestDigest + "\x00" + release.TreeDigest
}

var selectionInputSchema = json.RawMessage(`{"type":"object","additionalProperties":false,"required":["manifest-digest","profile","provider","repo-root","target","tree-digest","version"],"properties":{"manifest-digest":{"type":"string","pattern":"^sha256:[0-9a-f]{64}$"},"profile":{"type":"string","minLength":1},"provider":{"type":"string","minLength":1},"repo-root":{"type":"string","minLength":1},"target":{"type":"string","minLength":1},"tree-digest":{"type":"string","pattern":"^sha256:[0-9a-f]{64}$"},"version":{"type":"string","minLength":1}}}`)
var writeInputSchema = json.RawMessage(`{"type":"object","additionalProperties":false,"required":["manifest-digest","profile","provider","repo-root","target","tree-digest","version"],"properties":{"expected-plan-digest":{"type":"string","pattern":"^sha256:[0-9a-f]{64}$"},"manifest-digest":{"type":"string","pattern":"^sha256:[0-9a-f]{64}$"},"profile":{"type":"string","minLength":1},"provider":{"type":"string","minLength":1},"repo-root":{"type":"string","minLength":1},"target":{"type":"string","minLength":1},"tree-digest":{"type":"string","pattern":"^sha256:[0-9a-f]{64}$"},"version":{"type":"string","minLength":1}}}`)
var managedInputSchema = json.RawMessage(`{"type":"object","additionalProperties":false,"required":["provider","repo-root","target"],"properties":{"provider":{"type":"string","minLength":1},"repo-root":{"type":"string","minLength":1},"target":{"type":"string","minLength":1}}}`)

const outputDefinitions = `"$defs":{"fileState":{"type":"object","additionalProperties":false,"required":["type"],"properties":{"type":{"type":"string"},"mode":{"type":"integer","minimum":0},"size":{"type":"integer","minimum":0},"digest":{"type":"string"}}},"delta":{"type":"object","additionalProperties":false,"required":["path","kind","before","after"],"properties":{"path":{"type":"string"},"kind":{"type":"string"},"before":{"$ref":"#/$defs/fileState"},"after":{"$ref":"#/$defs/fileState"}}},"status":{"type":"object","additionalProperties":false,"required":["apiVersion","kind","state","snapshotDigest","deltas"],"properties":{"apiVersion":{"const":"nexa.dev/source-command-result/v1"},"kind":{"const":"SourceStatus"},"state":{"type":"string"},"snapshotDigest":{"type":"string"},"deltas":{"type":"array","items":{"$ref":"#/$defs/delta"}}}},"change":{"type":"object","additionalProperties":false,"required":["path","action","old","local","new","result"],"properties":{"path":{"type":"string"},"action":{"type":"string"},"old":{"$ref":"#/$defs/fileState"},"local":{"$ref":"#/$defs/fileState"},"new":{"$ref":"#/$defs/fileState"},"result":{"$ref":"#/$defs/fileState"}}},"conflict":{"type":"object","additionalProperties":false,"required":["path","reason","old","local","new"],"properties":{"path":{"type":"string"},"reason":{"type":"string"},"old":{"$ref":"#/$defs/fileState"},"local":{"$ref":"#/$defs/fileState"},"new":{"$ref":"#/$defs/fileState"}}},"plan":{"type":"object","additionalProperties":false,"required":["apiVersion","kind","operation","canApply","planDigest","beforeSnapshot","afterSnapshot","changes","conflicts"],"properties":{"apiVersion":{"const":"nexa.dev/source-command-result/v1"},"kind":{"const":"SourcePlan"},"operation":{"type":"string"},"canApply":{"type":"boolean"},"planDigest":{"type":"string"},"beforeSnapshot":{"type":"string"},"afterSnapshot":{"type":"string"},"changes":{"type":"array","items":{"$ref":"#/$defs/change"}},"conflicts":{"type":"array","items":{"$ref":"#/$defs/conflict"}}}}}`

var planOutputSchema = json.RawMessage(`{"type":"object","additionalProperties":false,"required":["apiVersion","kind","operation","canApply","planDigest","beforeSnapshot","afterSnapshot","changes","conflicts"],"properties":{"apiVersion":{"const":"nexa.dev/source-command-result/v1"},"kind":{"const":"SourcePlan"},"operation":{"type":"string"},"canApply":{"type":"boolean"},"planDigest":{"type":"string"},"beforeSnapshot":{"type":"string"},"afterSnapshot":{"type":"string"},"changes":{"type":"array","items":{"$ref":"#/$defs/change"}},"conflicts":{"type":"array","items":{"$ref":"#/$defs/conflict"}}},` + outputDefinitions + `}`)
var statusOutputSchema = json.RawMessage(`{"type":"object","additionalProperties":false,"required":["apiVersion","kind","state","snapshotDigest","deltas"],"properties":{"apiVersion":{"const":"nexa.dev/source-command-result/v1"},"kind":{"const":"SourceStatus"},"state":{"type":"string"},"snapshotDigest":{"type":"string"},"deltas":{"type":"array","items":{"$ref":"#/$defs/delta"}}},` + outputDefinitions + `}`)
var diffOutputSchema = json.RawMessage(`{"type":"object","additionalProperties":false,"required":["apiVersion","kind","snapshotDigest","deltas"],"properties":{"apiVersion":{"const":"nexa.dev/source-command-result/v1"},"kind":{"const":"SourceDiff"},"snapshotDigest":{"type":"string"},"deltas":{"type":"array","items":{"$ref":"#/$defs/delta"}}},` + outputDefinitions + `}`)
var checkOutputSchema = json.RawMessage(`{"type":"object","additionalProperties":false,"required":["apiVersion","kind","canApply","status","plan"],"properties":{"apiVersion":{"const":"nexa.dev/source-command-result/v1"},"kind":{"const":"SourceCheck"},"canApply":{"type":"boolean"},"status":{"$ref":"#/$defs/status"},"plan":{"$ref":"#/$defs/plan"}},` + outputDefinitions + `}`)
var resultOutputSchema = json.RawMessage(`{"type":"object","additionalProperties":false,"required":["apiVersion","kind","operation","status"],"properties":{"apiVersion":{"const":"nexa.dev/source-command-result/v1"},"kind":{"const":"SourceResult"},"operation":{"type":"string"},"planDigest":{"type":"string"},"lockDigest":{"type":"string"},"status":{"$ref":"#/$defs/status"}},` + outputDefinitions + `}`)

func commandSchemas(command string, releases ...sourceReleaseInspection) (json.RawMessage, json.RawMessage) {
	clone := func(value json.RawMessage) json.RawMessage { return append(json.RawMessage(nil), value...) }
	switch command {
	case "plan":
		return sourceSelectionSchema(selectionInputSchema, releases), clone(planOutputSchema)
	case "check":
		return sourceSelectionSchema(selectionInputSchema, releases), clone(checkOutputSchema)
	case "materialize", "upgrade":
		return sourceSelectionSchema(writeInputSchema, releases), clone(resultOutputSchema)
	case "status":
		return clone(managedInputSchema), clone(statusOutputSchema)
	case "diff":
		return clone(managedInputSchema), clone(diffOutputSchema)
	case "detach":
		return clone(managedInputSchema), clone(resultOutputSchema)
	default:
		return nil, nil
	}
}

func sourceSelectionSchema(base json.RawMessage, releases []sourceReleaseInspection) json.RawMessage {
	var document map[string]json.RawMessage
	if err := json.Unmarshal(base, &document); err != nil {
		return nil
	}
	values := append([]sourceReleaseInspection(nil), releases...)
	if values == nil {
		values = []sourceReleaseInspection{}
	}
	encoded, err := json.Marshal(values)
	if err != nil {
		return nil
	}
	document["x-nexa-source-releases"] = encoded
	result, err := json.Marshal(document)
	if err != nil {
		return nil
	}
	return result
}
