package sdkpythonassets

type WriteResult struct {
	APIVersion      string   `json:"apiVersion"`
	Changed         bool     `json:"changed"`
	IndexDigest     string   `json:"indexDigest"`
	BootstrapDigest string   `json:"bootstrapDigest"`
	Roles           []Role   `json:"roles"`
	ObjectsWritten  []string `json:"objectsWritten"`
	ObjectsReused   []string `json:"objectsReused"`
}

type CheckResult struct {
	APIVersion      string `json:"apiVersion"`
	IndexDigest     string `json:"indexDigest"`
	BootstrapDigest string `json:"bootstrapDigest"`
	Status          string `json:"status"`
	Roles           []Role `json:"roles"`
	ObjectCount     int    `json:"objectCount"`
}

type BuildResult struct {
	APIVersion      string `json:"apiVersion"`
	IndexDigest     string `json:"indexDigest"`
	BootstrapDigest string `json:"bootstrapDigest"`
	MatrixTarget    string `json:"matrixTarget"`
	PythonVersion   string `json:"pythonVersion"`
	PathBase        string `json:"pathBase"`
	WheelPath       string `json:"wheelPath"`
	WheelDigest     string `json:"wheelDigest"`
	WheelSize       int64  `json:"wheelSize"`
	RecordDigest    string `json:"recordDigest"`
	Roles           []Role `json:"roles"`
}

func cloneRoles(roles []Role) []Role { return append([]Role(nil), roles...) }
