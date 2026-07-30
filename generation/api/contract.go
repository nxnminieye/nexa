package api

// HTTPMethod is the minimal compiler-local operation method used by the
// canonical .api and frontend closure pipelines.
type HTTPMethod string

const (
	MethodGET    HTTPMethod = "GET"
	MethodPOST   HTTPMethod = "POST"
	MethodPUT    HTTPMethod = "PUT"
	MethodPATCH  HTTPMethod = "PATCH"
	MethodDELETE HTTPMethod = "DELETE"
)

type AuthMode string

const (
	AuthNone     AuthMode = "none"
	AuthRequired AuthMode = "required"
)
