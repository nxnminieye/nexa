package protocol

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
	AuthOptional AuthMode = "optional"
	AuthRequired AuthMode = "required"
)

type CredentialType string

const (
	CredentialBearer        CredentialType = "bearer"
	CredentialAPIKey        CredentialType = "api-key"
	CredentialSessionCookie CredentialType = "session-cookie"
)

type CredentialLocation string

const (
	CredentialHeader CredentialLocation = "header"
	CredentialQuery  CredentialLocation = "query"
	CredentialCookie CredentialLocation = "cookie"
)

type ContextValue string

const (
	ContextSubjectID ContextValue = "subject-id"
	ContextTenantID  ContextValue = "tenant-id"
	ContextRequestID ContextValue = "request-id"
	ContextTraceID   ContextValue = "trace-id"
)

type Auth struct{ state *authState }
type Credential struct{ state *credentialState }
type RequestFieldBinding struct{ state *requestFieldState }
type ContextBinding struct{ state *contextFieldState }
type ResponseFieldBinding struct{ state *responseFieldState }
type ErrorProjection struct{ state *errorProjectionState }
type ErrorMatch struct{ state *errorMatchState }
type ErrorTarget struct{ state *errorTargetState }

type authState struct {
	mode        AuthMode
	credentials []*credentialState
}
type credentialState struct {
	id       string
	typeID   CredentialType
	location CredentialLocation
	name     string
}
type requestFieldState struct {
	httpField string
	rpcPath   []string
}
type contextFieldState struct {
	source  ContextValue
	rpcPath []string
}
type responseFieldState struct {
	httpField string
	rpcPath   []string
}
type errorProjectionState struct {
	match   errorMatchState
	project errorTargetState
}
type errorMatchState struct{ domain, code string }
type errorTargetState struct {
	domain, code string
	httpStatus   int
}

func (p HTTPProxy) OperationID() string {
	if p.state == nil {
		return ""
	}
	return p.state.operationID
}
func (p HTTPProxy) Method() HTTPMethod {
	if p.state == nil {
		return ""
	}
	return p.state.method
}
func (p HTTPProxy) Path() string {
	if p.state == nil {
		return ""
	}
	return p.state.path
}
func (p HTTPProxy) Permission() string {
	if p.state == nil {
		return ""
	}
	return p.state.permission
}
func (p HTTPProxy) Auth() Auth {
	if p.state == nil {
		return Auth{}
	}
	return Auth{state: &p.state.auth}
}
func (p HTTPProxy) RequestFields() []RequestFieldBinding {
	if p.state == nil {
		return nil
	}
	result := make([]RequestFieldBinding, len(p.state.requestFields))
	for i, value := range p.state.requestFields {
		result[i] = RequestFieldBinding{state: value}
	}
	return result
}
func (c RPCContext) ContextFields() []ContextBinding {
	if c.state == nil {
		return nil
	}
	result := make([]ContextBinding, len(c.state.contextFields))
	for i, value := range c.state.contextFields {
		result[i] = ContextBinding{state: value}
	}
	return result
}
func (p HTTPProxy) ResponseFields() []ResponseFieldBinding {
	if p.state == nil {
		return nil
	}
	result := make([]ResponseFieldBinding, len(p.state.responseFields))
	for i, value := range p.state.responseFields {
		result[i] = ResponseFieldBinding{state: value}
	}
	return result
}
func (p HTTPProxy) Errors() []ErrorProjection {
	if p.state == nil {
		return nil
	}
	result := make([]ErrorProjection, len(p.state.errors))
	for i, value := range p.state.errors {
		result[i] = ErrorProjection{state: value}
	}
	return result
}
func (a Auth) Mode() AuthMode {
	if a.state == nil {
		return ""
	}
	return a.state.mode
}
func (a Auth) Credentials() []Credential {
	if a.state == nil {
		return nil
	}
	result := make([]Credential, len(a.state.credentials))
	for i, value := range a.state.credentials {
		result[i] = Credential{state: value}
	}
	return result
}
func (c Credential) ID() string {
	if c.state == nil {
		return ""
	}
	return c.state.id
}
func (c Credential) Type() CredentialType {
	if c.state == nil {
		return ""
	}
	return c.state.typeID
}
func (c Credential) Location() CredentialLocation {
	if c.state == nil {
		return ""
	}
	return c.state.location
}
func (c Credential) Name() string {
	if c.state == nil {
		return ""
	}
	return c.state.name
}
func (b RequestFieldBinding) HTTPField() string {
	if b.state == nil {
		return ""
	}
	return b.state.httpField
}
func (b RequestFieldBinding) RPCPath() []string {
	if b.state == nil {
		return nil
	}
	return append([]string(nil), b.state.rpcPath...)
}
func (b ContextBinding) Source() ContextValue {
	if b.state == nil {
		return ""
	}
	return b.state.source
}
func (b ContextBinding) RPCPath() []string {
	if b.state == nil {
		return nil
	}
	return append([]string(nil), b.state.rpcPath...)
}
func (b ResponseFieldBinding) HTTPField() string {
	if b.state == nil {
		return ""
	}
	return b.state.httpField
}
func (b ResponseFieldBinding) RPCPath() []string {
	if b.state == nil {
		return nil
	}
	return append([]string(nil), b.state.rpcPath...)
}
func (e ErrorProjection) Match() ErrorMatch {
	if e.state == nil {
		return ErrorMatch{}
	}
	return ErrorMatch{state: &e.state.match}
}
func (e ErrorProjection) Project() ErrorTarget {
	if e.state == nil {
		return ErrorTarget{}
	}
	return ErrorTarget{state: &e.state.project}
}
func (m ErrorMatch) Domain() string {
	if m.state == nil {
		return ""
	}
	return m.state.domain
}
func (m ErrorMatch) Code() string {
	if m.state == nil {
		return ""
	}
	return m.state.code
}
func (p ErrorTarget) Domain() string {
	if p.state == nil {
		return ""
	}
	return p.state.domain
}
func (p ErrorTarget) Code() string {
	if p.state == nil {
		return ""
	}
	return p.state.code
}
func (p ErrorTarget) HTTPStatus() int {
	if p.state == nil {
		return 0
	}
	return p.state.httpStatus
}
