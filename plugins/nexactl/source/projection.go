package source

import "github.com/nxnminieye/nexa/sourceplugin/engine"

const resultAPIVersion = "nexa.dev/source-command-result/v1"

type fileStateProjection struct {
	Type   engine.FileType `json:"type"`
	Mode   uint32          `json:"mode,omitempty"`
	Size   int64           `json:"size,omitempty"`
	Digest string          `json:"digest,omitempty"`
}

type deltaProjection struct {
	Path   string              `json:"path"`
	Kind   engine.DeltaKind    `json:"kind"`
	Before fileStateProjection `json:"before"`
	After  fileStateProjection `json:"after"`
}

type statusProjection struct {
	APIVersion     string              `json:"apiVersion"`
	Kind           string              `json:"kind"`
	State          engine.ManagedState `json:"state"`
	SnapshotDigest string              `json:"snapshotDigest"`
	Deltas         []deltaProjection   `json:"deltas"`
}

type changeProjection struct {
	Path   string              `json:"path"`
	Action engine.ChangeAction `json:"action"`
	Old    fileStateProjection `json:"old"`
	Local  fileStateProjection `json:"local"`
	New    fileStateProjection `json:"new"`
	Result fileStateProjection `json:"result"`
}

type conflictProjection struct {
	Path   string                `json:"path"`
	Reason engine.ConflictReason `json:"reason"`
	Old    fileStateProjection   `json:"old"`
	Local  fileStateProjection   `json:"local"`
	New    fileStateProjection   `json:"new"`
}

type planProjection struct {
	APIVersion     string               `json:"apiVersion"`
	Kind           string               `json:"kind"`
	Operation      engine.PlanOperation `json:"operation"`
	CanApply       bool                 `json:"canApply"`
	PlanDigest     string               `json:"planDigest"`
	BeforeSnapshot string               `json:"beforeSnapshot"`
	AfterSnapshot  string               `json:"afterSnapshot"`
	Changes        []changeProjection   `json:"changes"`
	Conflicts      []conflictProjection `json:"conflicts"`
}

type checkProjection struct {
	APIVersion string           `json:"apiVersion"`
	Kind       string           `json:"kind"`
	CanApply   bool             `json:"canApply"`
	Status     statusProjection `json:"status"`
	Plan       planProjection   `json:"plan"`
}

type resultProjection struct {
	APIVersion string               `json:"apiVersion"`
	Kind       string               `json:"kind"`
	Operation  engine.PlanOperation `json:"operation"`
	PlanDigest string               `json:"planDigest,omitempty"`
	LockDigest string               `json:"lockDigest,omitempty"`
	Status     statusProjection     `json:"status"`
}

func projectPlan(value engine.Plan) any {
	changes := value.Changes()
	projectedChanges := make([]changeProjection, len(changes))
	for index, change := range changes {
		projectedChanges[index] = changeProjection{
			Path: change.Path(), Action: change.Action(), Old: projectFileState(change.Old()), Local: projectFileState(change.Local()),
			New: projectFileState(change.New()), Result: projectFileState(change.Result()),
		}
	}
	conflicts := value.Conflicts()
	projectedConflicts := make([]conflictProjection, len(conflicts))
	for index, conflict := range conflicts {
		projectedConflicts[index] = conflictProjection{
			Path: conflict.Path(), Reason: conflict.Reason(), Old: projectFileState(conflict.Old()),
			Local: projectFileState(conflict.Local()), New: projectFileState(conflict.New()),
		}
	}
	return planProjection{
		APIVersion: resultAPIVersion, Kind: "SourcePlan", Operation: value.Operation(), CanApply: value.CanApply(),
		PlanDigest: value.Digest().String(), BeforeSnapshot: value.BeforeDigest().String(), AfterSnapshot: value.AfterDigest().String(),
		Changes: projectedChanges, Conflicts: projectedConflicts,
	}
}

func projectStatus(value engine.Status) any {
	return projectStatusValue(value, "SourceStatus")
}

func projectDiff(value engine.Diff) any {
	deltas := value.Deltas()
	projected := make([]deltaProjection, len(deltas))
	for index, delta := range deltas {
		projected[index] = projectDelta(delta)
	}
	return struct {
		APIVersion     string            `json:"apiVersion"`
		Kind           string            `json:"kind"`
		SnapshotDigest string            `json:"snapshotDigest"`
		Deltas         []deltaProjection `json:"deltas"`
	}{resultAPIVersion, "SourceDiff", value.SnapshotDigest().String(), projected}
}

func projectCheck(value engine.Check) any {
	return checkProjection{
		APIVersion: resultAPIVersion, Kind: "SourceCheck", CanApply: value.CanApply(),
		Status: projectStatusValue(value.Status(), "SourceStatus"), Plan: projectPlan(value.Plan()).(planProjection),
	}
}

func projectResult(value engine.Result) any {
	return resultProjection{
		APIVersion: resultAPIVersion, Kind: "SourceResult", Operation: value.Operation(),
		PlanDigest: value.PlanDigest().String(), LockDigest: value.LockDigest().String(),
		Status: projectStatusValue(value.Status(), "SourceStatus"),
	}
}

func projectStatusValue(value engine.Status, kind string) statusProjection {
	deltas := value.Deltas()
	projected := make([]deltaProjection, len(deltas))
	for index, delta := range deltas {
		projected[index] = projectDelta(delta)
	}
	return statusProjection{
		APIVersion: resultAPIVersion, Kind: kind, State: value.State(),
		SnapshotDigest: value.SnapshotDigest().String(), Deltas: projected,
	}
}

func projectDelta(value engine.Delta) deltaProjection {
	return deltaProjection{Path: value.Path(), Kind: value.Kind(), Before: projectFileState(value.Before()), After: projectFileState(value.After())}
}

func projectFileState(value engine.FileState) fileStateProjection {
	return fileStateProjection{Type: value.Type(), Mode: value.Mode(), Size: value.Size(), Digest: value.Digest().String()}
}
