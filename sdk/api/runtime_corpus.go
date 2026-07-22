package api

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"errors"
	"net/url"

	"github.com/gowebpki/jcs"
	generationapi "github.com/nxnminieye/nexa/generation/api"
	"github.com/nxnminieye/nexa/internal/strictdoc"
)

const (
	RuntimeCorpusAPIVersion = "nexa.dev/runtime-api-conformance/v1"
	RuntimeCorpusRawBytes   = 65_536
)

func runtimeCorpusRequestRoster() []string {
	return []string{
		"sample-get-valid", "utf16-member-order", "duplicate-id", "nested-null",
	}
}

func runtimeCorpusCredentialRoster() []string {
	return []string{
		"primary-present", "primary-missing", "count-first-mixed-invalid",
	}
}

func runtimeCorpusWireRequestRoster() []string { return []string{"sample-get-path-bearer"} }

func runtimeCorpusResponseRoster() []string {
	return []string{
		"base-media-type", "vendor-json", "utf8-charset", "duplicate-general-headers", "none-does-not-read",
		"status-below-constructor-range", "status-upper-bound", "content-type-missing", "content-type-duplicate",
		"content-type-malformed", "content-type-unsupported", "content-type-parameter-invalid", "body-empty",
		"body-too-large", "body-document-invalid", "body-duplicate-key", "body-trailing-input", "body-schema-invalid",
	}
}

func runtimeCorpusResponseLimitRoster() []string {
	return []string{
		"depth-inclusive-64", "depth-one-over-65", "nodes-inclusive-65536", "nodes-one-over-65537",
	}
}

func runtimeCorpusRemoteGrammarRoster() []string {
	return []string{
		"domain-trailing-lf", "code-trailing-lf", "message-trailing-lf", "request-id-trailing-lf", "trace-id-trailing-lf",
	}
}

func runtimeCorpusRemoteLimitRoster() []string {
	return []string{"recursive-member-total-256", "recursive-member-total-257"}
}

func runtimeCorpusErrorRoster() []string {
	return []string{
		"sample-not-found", "rfc8785-number-canonicalization", "malformed-remote-error",
	}
}

func runtimeCorpusContractBaseRoster() []string { return []string{"simple", "relation"} }

func runtimeCorpusContractCaseRoster() []string {
	return []string{
		"schema-index-invalid", "schema-duplicate", "operation-schema-index-invalid", "request-schema-kind-invalid",
		"binding-field-unresolved", "binding-field-missing", "binding-schema-kind-invalid", "path-field-optional",
		"path-binding-mismatch", "path-binding-name-invalid", "path-invalid", "binding-wire-target-duplicate",
		"header-name-reserved", "credential-combination-invalid", "credential-wire-target-duplicate",
		"credential-binding-conflict", "permission-auth-conflict", "capability-version-invalid",
		"compound-schema-before-duplicate", "compound-request-before-binding", "compound-unresolved-before-missing",
		"compound-scalar-before-wire", "compound-auth-before-credential", "compound-permission-before-capability",
		"schema-array-items-invalid", "response-schema-index-invalid", "bearer-location-invalid", "bearer-name-invalid",
		"session-cookie-location-invalid", "cookie-credential-binding-conflict",
		"compound-response-before-binding", "compound-request-kind-before-binding", "compound-missing-before-scalar",
		"compound-path-required-before-set", "compound-path-set-before-name", "compound-path-name-before-shape",
		"compound-wire-before-reserved", "compound-reserved-before-credential", "compound-credential-in-before-name",
		"compound-credential-combination-before-wire", "compound-credential-wire-before-binding",
		"compound-binding-before-permission",
	}
}

func runtimeCorpusAdapterCaseRoster() []string {
	return []string{
		"success-json", "success-json-one-byte", "success-none-close-panic", "mapped-remote-error-close-failure",
		"request-field-required", "provider-failure", "transport-failure", "response-and-failure", "body-read-failure",
		"body-size-limit", "pre-call-canceled", "pre-call-deadline", "transport-canceled", "body-read-canceled",
		"invalid-response-header-name", "invalid-response-header-value", "required-response-body", "provider-panic",
		"provider-canceled", "provider-deadline", "transport-panic", "zero-response", "transport-deadline",
		"body-read-panic", "body-read-deadline",
	}
}

//go:embed testdata/runtime-api-v1.json
var embeddedRuntimeCorpus []byte

type RuntimeAdapterContextBehavior string
type RuntimeAdapterProviderBehavior string
type RuntimeAdapterTransportBehavior string
type RuntimeAdapterReadBehavior string
type RuntimeAdapterCloseBehavior string
type runtimeContractMutationOperation string

const (
	RuntimeAdapterContextActive   RuntimeAdapterContextBehavior = "active"
	RuntimeAdapterContextCanceled RuntimeAdapterContextBehavior = "canceled"
	RuntimeAdapterContextDeadline RuntimeAdapterContextBehavior = "deadline"

	RuntimeAdapterProviderValues   RuntimeAdapterProviderBehavior = "values"
	RuntimeAdapterProviderFailure  RuntimeAdapterProviderBehavior = "failure"
	RuntimeAdapterProviderPanic    RuntimeAdapterProviderBehavior = "panic"
	RuntimeAdapterProviderCancel   RuntimeAdapterProviderBehavior = "cancel"
	RuntimeAdapterProviderDeadline RuntimeAdapterProviderBehavior = "deadline"

	RuntimeAdapterTransportResponse           RuntimeAdapterTransportBehavior = "response"
	RuntimeAdapterTransportFailure            RuntimeAdapterTransportBehavior = "failure"
	RuntimeAdapterTransportPanic              RuntimeAdapterTransportBehavior = "panic"
	RuntimeAdapterTransportResponseAndFailure RuntimeAdapterTransportBehavior = "response-and-failure"
	RuntimeAdapterTransportZero               RuntimeAdapterTransportBehavior = "zero"
	RuntimeAdapterTransportCancel             RuntimeAdapterTransportBehavior = "cancel"
	RuntimeAdapterTransportDeadline           RuntimeAdapterTransportBehavior = "deadline"

	RuntimeAdapterReadAll       RuntimeAdapterReadBehavior = "all"
	RuntimeAdapterReadOneByte   RuntimeAdapterReadBehavior = "one-byte"
	RuntimeAdapterReadFailure   RuntimeAdapterReadBehavior = "failure"
	RuntimeAdapterReadPanic     RuntimeAdapterReadBehavior = "panic"
	RuntimeAdapterReadCancel    RuntimeAdapterReadBehavior = "cancel"
	RuntimeAdapterReadDeadline  RuntimeAdapterReadBehavior = "deadline"
	RuntimeAdapterReadForbidden RuntimeAdapterReadBehavior = "forbidden"
	RuntimeAdapterReadAbsent    RuntimeAdapterReadBehavior = "absent"

	RuntimeAdapterCloseSuccess RuntimeAdapterCloseBehavior = "success"
	RuntimeAdapterCloseFailure RuntimeAdapterCloseBehavior = "failure"
	RuntimeAdapterClosePanic   RuntimeAdapterCloseBehavior = "panic"

	runtimeContractMutationSet    runtimeContractMutationOperation = "set"
	runtimeContractMutationRemove runtimeContractMutationOperation = "remove"
	runtimeContractMutationAppend runtimeContractMutationOperation = "append"
)

// RuntimeCorpusCredentialValue is one typed fake credential input.
type RuntimeCorpusCredentialValue struct {
	ID    string `json:"id"`
	Value string `json:"value"`
}

// RuntimeAdapterTransport describes one closed fake transport and body behavior.
type RuntimeAdapterTransport struct {
	Behavior      RuntimeAdapterTransportBehavior `json:"behavior"`
	Status        int                             `json:"status"`
	Headers       []RuntimeAdapterHeader          `json:"headers"`
	Body          string                          `json:"body"`
	ReadBehavior  RuntimeAdapterReadBehavior      `json:"readBehavior"`
	CloseBehavior RuntimeAdapterCloseBehavior     `json:"closeBehavior"`
}

// RuntimeAdapterExpected is the independently authored owner expectation.
type RuntimeAdapterExpected struct {
	Request        *RuntimeAdapterRequest `json:"request"`
	ProviderCalls  int                    `json:"providerCalls"`
	TransportCalls int                    `json:"transportCalls"`
	BodyReadCalls  int                    `json:"bodyReadCalls"`
	BodyCloseCalls int                    `json:"bodyCloseCalls"`
	Outcome        RuntimeAdapterOutcome  `json:"outcome"`
}

// RuntimeAdapterCase is one closed conformance invocation.
type RuntimeAdapterCase struct {
	Name             string                         `json:"name"`
	Endpoint         string                         `json:"endpoint"`
	APIOperationID   string                         `json:"apiOperationId"`
	Request          string                         `json:"request"`
	Credentials      []RuntimeCorpusCredentialValue `json:"credentials"`
	MaxResponseBytes int64                          `json:"maxResponseBytes"`
	ContextBehavior  RuntimeAdapterContextBehavior  `json:"contextBehavior"`
	ProviderBehavior RuntimeAdapterProviderBehavior `json:"providerBehavior"`
	Transport        RuntimeAdapterTransport        `json:"transport"`
	Expected         RuntimeAdapterExpected         `json:"expected"`
}

type runtimeCorpusLegacyError struct {
	Code    string `json:"code"`
	Reason  string `json:"reason"`
	Pointer string `json:"pointer"`
}

type runtimeCorpusRequestCase struct {
	Name      string                    `json:"name"`
	JSON      string                    `json:"json"`
	Valid     bool                      `json:"valid"`
	Canonical string                    `json:"canonical,omitempty"`
	Error     *runtimeCorpusLegacyError `json:"error,omitempty"`
}

type runtimeCorpusCredentialCase struct {
	Name   string                         `json:"name"`
	Values []RuntimeCorpusCredentialValue `json:"values"`
	Valid  bool                           `json:"valid"`
	Error  *runtimeCorpusLegacyError      `json:"error,omitempty"`
}

type runtimeCorpusWireExpected struct {
	Method      string                 `json:"method"`
	Path        string                 `json:"path"`
	RawPath     string                 `json:"rawPath"`
	EscapedPath string                 `json:"escapedPath"`
	RequestURI  string                 `json:"requestUri"`
	Headers     []RuntimeAdapterHeader `json:"headers"`
	Body        *string                `json:"body"`
}

type runtimeCorpusWireCase struct {
	Name           string                         `json:"name"`
	Endpoint       string                         `json:"endpoint"`
	APIOperationID string                         `json:"apiOperationId"`
	Request        string                         `json:"request"`
	Credentials    []RuntimeCorpusCredentialValue `json:"credentials"`
	Expected       runtimeCorpusWireExpected      `json:"expected"`
}

type runtimeCorpusResponseCase struct {
	Name             string                         `json:"name"`
	ResponseBody     generationapi.ResponseBodyMode `json:"responseBody"`
	Status           int                            `json:"status"`
	Headers          []RuntimeAdapterHeader         `json:"headers"`
	Body             string                         `json:"body"`
	MaxResponseBytes int64                          `json:"maxResponseBytes,omitempty"`
	Valid            bool                           `json:"valid"`
	HasJSON          *bool                          `json:"hasJSON,omitempty"`
	Canonical        string                         `json:"canonical,omitempty"`
	Error            *runtimeCorpusLegacyError      `json:"error,omitempty"`
}

type runtimeCorpusLimitRecipe struct {
	Name   string                    `json:"name"`
	Kind   string                    `json:"kind"`
	Amount int                       `json:"amount"`
	Valid  bool                      `json:"valid"`
	Error  *runtimeCorpusLegacyError `json:"error,omitempty"`
}

type runtimeCorpusGrammarCase struct {
	Name  string                   `json:"name"`
	Field string                   `json:"field"`
	Body  string                   `json:"body"`
	Valid bool                     `json:"valid"`
	Error runtimeCorpusLegacyError `json:"error"`
}

type runtimeCorpusRemoteLimitRecipe struct {
	Name                string                    `json:"name"`
	Kind                string                    `json:"kind"`
	FirstObjectMembers  int                       `json:"firstObjectMembers"`
	SecondObjectMembers int                       `json:"secondObjectMembers"`
	Valid               bool                      `json:"valid"`
	Error               *runtimeCorpusLegacyError `json:"error,omitempty"`
}

type runtimeCorpusProjectedError struct {
	Domain     string `json:"domain"`
	Code       string `json:"code"`
	HTTPStatus int    `json:"httpStatus"`
}

type runtimeCorpusRemoteErrorCase struct {
	Name      string                       `json:"name"`
	Status    int                          `json:"status"`
	Body      string                       `json:"body"`
	Valid     bool                         `json:"valid"`
	Projected *runtimeCorpusProjectedError `json:"projected,omitempty"`
	Canonical string                       `json:"canonical,omitempty"`
	Error     *runtimeCorpusLegacyError    `json:"error,omitempty"`
}

type runtimeContractCorpusBase struct {
	Name     string `json:"name"`
	Document string `json:"document"`
}

type runtimeContractCorpusMutation struct {
	Operation runtimeContractMutationOperation `json:"operation"`
	Pointer   string                           `json:"pointer"`
	ValueJSON string                           `json:"valueJSON"`
}

type runtimeContractCorpusCase struct {
	Name      string                          `json:"name"`
	Base      string                          `json:"base"`
	Compound  bool                            `json:"compound"`
	Mutations []runtimeContractCorpusMutation `json:"mutations"`
	Expected  runtimeCorpusLegacyError        `json:"expected"`
}

type runtimeCorpusDocument struct {
	APIVersion              string                           `json:"apiVersion"`
	Manifest                string                           `json:"manifest"`
	RuntimeLimits           RuntimeLimitSet                  `json:"runtimeLimits"`
	RuntimeLimitSemantics   runtimeLimitSemanticsDocument    `json:"runtimeLimitSemantics"`
	Requests                []runtimeCorpusRequestCase       `json:"requests"`
	Credentials             []runtimeCorpusCredentialCase    `json:"credentials"`
	WireRequests            []runtimeCorpusWireCase          `json:"wireRequests"`
	Responses               []runtimeCorpusResponseCase      `json:"responses"`
	ResponseLimitRecipes    []runtimeCorpusLimitRecipe       `json:"responseLimitRecipes"`
	RemoteErrorGrammar      []runtimeCorpusGrammarCase       `json:"remoteErrorGrammar"`
	RemoteErrorLimitRecipes []runtimeCorpusRemoteLimitRecipe `json:"remoteErrorLimitRecipes"`
	Errors                  []runtimeCorpusRemoteErrorCase   `json:"errors"`
	RuntimeContractBases    []runtimeContractCorpusBase      `json:"runtimeContractBases"`
	RuntimeContractCases    []runtimeContractCorpusCase      `json:"runtimeContractCases"`
	AdapterCases            []RuntimeAdapterCase             `json:"adapterCases"`
}

// RuntimeCorpus is one strict typed conformance projection.
type RuntimeCorpus struct{ document runtimeCorpusDocument }

// RuntimeCorpusBytes returns fresh canonical bytes from the strict owner fixture.
func RuntimeCorpusBytes() ([]byte, error) {
	corpus, err := ParseRuntimeCorpus(embeddedRuntimeCorpus)
	if err != nil {
		return nil, err
	}
	return corpus.CanonicalJSON()
}

// ParseRuntimeCorpus validates every closed section and freezes its values.
func ParseRuntimeCorpus(data []byte) (RuntimeCorpus, error) {
	if len(data) > RuntimeCorpusRawBytes {
		return RuntimeCorpus{}, errors.New("runtime corpus is invalid")
	}
	document, err := strictdoc.ParseJSON("runtime-corpus.json", data)
	if err != nil {
		return RuntimeCorpus{}, errors.New("runtime corpus is invalid")
	}
	var generic any
	decoder := json.NewDecoder(bytes.NewReader(document.JSON()))
	decoder.UseNumber()
	if err := decoder.Decode(&generic); err != nil {
		return RuntimeCorpus{}, errors.New("runtime corpus is invalid")
	}
	schema, err := runtimeCorpusDocumentSchema()
	if err != nil || schema.Validate(generic) != nil {
		return RuntimeCorpus{}, errors.New("runtime corpus is invalid")
	}
	var decoded runtimeCorpusDocument
	if err := document.Decode(&decoded); err != nil || validateRuntimeCorpusDocument(decoded) != nil {
		return RuntimeCorpus{}, errors.New("runtime corpus is invalid")
	}
	return RuntimeCorpus{document: cloneRuntimeCorpusDocument(decoded)}, nil
}

// APIVersion returns the corpus contract identity.
func (c RuntimeCorpus) APIVersion() string { return c.document.APIVersion }

// ManifestJSON returns the opaque canonical Manifest fixture used only by Go owner tests.
func (c RuntimeCorpus) ManifestJSON() []byte { return append([]byte(nil), c.document.Manifest...) }

// AdapterCases returns independent typed conformance cases.
func (c RuntimeCorpus) AdapterCases() []RuntimeAdapterCase {
	return cloneRuntimeAdapterCases(c.document.AdapterCases)
}

// ExpectedAdapterResult returns the independently authored owner expectation.
func (c RuntimeCorpus) ExpectedAdapterResult() (RuntimeAdapterResult, error) {
	rows := make([]RuntimeAdapterCaseResult, len(c.document.AdapterCases))
	for index, test := range c.document.AdapterCases {
		requestDigest := "absent"
		if test.Expected.Request != nil {
			digest, err := test.Expected.Request.Digest()
			if err != nil {
				return RuntimeAdapterResult{}, errors.New("runtime corpus is invalid")
			}
			requestDigest = digest.String()
		}
		rows[index] = RuntimeAdapterCaseResult{
			Name: test.Name, RequestDigest: requestDigest, ProviderCalls: test.Expected.ProviderCalls,
			TransportCalls: test.Expected.TransportCalls, BodyReadCalls: test.Expected.BodyReadCalls,
			BodyCloseCalls: test.Expected.BodyCloseCalls, Outcome: test.Expected.Outcome,
		}
	}
	return NewRuntimeAdapterResult(rows)
}

// CanonicalJSON returns exact RFC 8785 corpus bytes.
func (c RuntimeCorpus) CanonicalJSON() ([]byte, error) {
	if err := validateRuntimeCorpusDocument(c.document); err != nil {
		return nil, errors.New("runtime corpus is invalid")
	}
	encoded, err := json.Marshal(c.document)
	if err != nil {
		return nil, errors.New("runtime corpus is invalid")
	}
	canonical, err := jcs.Transform(encoded)
	if err != nil {
		return nil, errors.New("runtime corpus is invalid")
	}
	return canonical, nil
}

func validateRuntimeCorpusDocument(document runtimeCorpusDocument) error {
	if document.APIVersion != RuntimeCorpusAPIVersion || document.Manifest == "" {
		return errors.New("runtime corpus is invalid")
	}
	projection := runtimeCorpusProjectionDocument{RuntimeLimits: document.RuntimeLimits, RuntimeLimitSemantics: document.RuntimeLimitSemantics}
	projectionJSON, err := canonicalRuntimeCorpusProjection(projection)
	if err != nil || CheckRuntimeCorpusProjection(projectionJSON) != nil {
		return errors.New("runtime corpus is invalid")
	}
	if !runtimeCorpusRosterMatches(document.Requests, runtimeCorpusRequestRoster(), func(row runtimeCorpusRequestCase) string { return row.Name }) ||
		!runtimeCorpusRosterMatches(document.Credentials, runtimeCorpusCredentialRoster(), func(row runtimeCorpusCredentialCase) string { return row.Name }) ||
		!runtimeCorpusRosterMatches(document.WireRequests, runtimeCorpusWireRequestRoster(), func(row runtimeCorpusWireCase) string { return row.Name }) ||
		!runtimeCorpusRosterMatches(document.Responses, runtimeCorpusResponseRoster(), func(row runtimeCorpusResponseCase) string { return row.Name }) ||
		!runtimeCorpusRosterMatches(document.ResponseLimitRecipes, runtimeCorpusResponseLimitRoster(), func(row runtimeCorpusLimitRecipe) string { return row.Name }) ||
		!runtimeCorpusRosterMatches(document.RemoteErrorGrammar, runtimeCorpusRemoteGrammarRoster(), func(row runtimeCorpusGrammarCase) string { return row.Name }) ||
		!runtimeCorpusRosterMatches(document.RemoteErrorLimitRecipes, runtimeCorpusRemoteLimitRoster(), func(row runtimeCorpusRemoteLimitRecipe) string { return row.Name }) ||
		!runtimeCorpusRosterMatches(document.Errors, runtimeCorpusErrorRoster(), func(row runtimeCorpusRemoteErrorCase) string { return row.Name }) ||
		!runtimeCorpusRosterMatches(document.RuntimeContractBases, runtimeCorpusContractBaseRoster(), func(row runtimeContractCorpusBase) string { return row.Name }) ||
		!runtimeCorpusRosterMatches(document.RuntimeContractCases, runtimeCorpusContractCaseRoster(), func(row runtimeContractCorpusCase) string { return row.Name }) ||
		!runtimeCorpusRosterMatches(document.AdapterCases, runtimeCorpusAdapterCaseRoster(), func(row RuntimeAdapterCase) string { return row.Name }) {
		return errors.New("runtime corpus is invalid")
	}
	seen := make(map[string]struct{}, len(document.AdapterCases))
	contextBehaviors := make(map[RuntimeAdapterContextBehavior]struct{})
	providerBehaviors := make(map[RuntimeAdapterProviderBehavior]struct{})
	transportBehaviors := make(map[RuntimeAdapterTransportBehavior]struct{})
	readBehaviors := make(map[RuntimeAdapterReadBehavior]struct{})
	closeBehaviors := make(map[RuntimeAdapterCloseBehavior]struct{})
	for _, test := range document.AdapterCases {
		if test.Name == "" || !validRuntimeAdapterEndpoint(test.Endpoint) {
			return errors.New("runtime corpus is invalid")
		}
		if _, duplicate := seen[test.Name]; duplicate {
			return errors.New("runtime corpus is invalid")
		}
		seen[test.Name] = struct{}{}
		contextBehaviors[test.ContextBehavior] = struct{}{}
		providerBehaviors[test.ProviderBehavior] = struct{}{}
		transportBehaviors[test.Transport.Behavior] = struct{}{}
		readBehaviors[test.Transport.ReadBehavior] = struct{}{}
		closeBehaviors[test.Transport.CloseBehavior] = struct{}{}
	}
	if !runtimeCorpusCovers(contextBehaviors, []RuntimeAdapterContextBehavior{
		RuntimeAdapterContextActive, RuntimeAdapterContextCanceled, RuntimeAdapterContextDeadline,
	}) || !runtimeCorpusCovers(providerBehaviors, []RuntimeAdapterProviderBehavior{
		RuntimeAdapterProviderValues, RuntimeAdapterProviderFailure, RuntimeAdapterProviderPanic,
		RuntimeAdapterProviderCancel, RuntimeAdapterProviderDeadline,
	}) || !runtimeCorpusCovers(transportBehaviors, []RuntimeAdapterTransportBehavior{
		RuntimeAdapterTransportResponse, RuntimeAdapterTransportFailure, RuntimeAdapterTransportPanic,
		RuntimeAdapterTransportResponseAndFailure, RuntimeAdapterTransportZero,
		RuntimeAdapterTransportCancel, RuntimeAdapterTransportDeadline,
	}) || !runtimeCorpusCovers(readBehaviors, []RuntimeAdapterReadBehavior{
		RuntimeAdapterReadAll, RuntimeAdapterReadOneByte, RuntimeAdapterReadFailure, RuntimeAdapterReadPanic,
		RuntimeAdapterReadCancel, RuntimeAdapterReadDeadline, RuntimeAdapterReadForbidden, RuntimeAdapterReadAbsent,
	}) || !runtimeCorpusCovers(closeBehaviors, []RuntimeAdapterCloseBehavior{
		RuntimeAdapterCloseSuccess, RuntimeAdapterCloseFailure, RuntimeAdapterClosePanic,
	}) {
		return errors.New("runtime corpus is invalid")
	}
	bases := make(map[string]struct{}, len(document.RuntimeContractBases))
	for _, base := range document.RuntimeContractBases {
		if base.Name == "" || base.Document == "" {
			return errors.New("runtime corpus is invalid")
		}
		if _, duplicate := bases[base.Name]; duplicate {
			return errors.New("runtime corpus is invalid")
		}
		bases[base.Name] = struct{}{}
		if _, err := ParseRuntimeContract([]byte(base.Document)); err != nil {
			return errors.New("runtime corpus is invalid")
		}
	}
	contractCases := make(map[string]struct{}, len(document.RuntimeContractCases))
	for _, test := range document.RuntimeContractCases {
		if test.Name == "" || len(test.Mutations) == 0 {
			return errors.New("runtime corpus is invalid")
		}
		if _, exists := bases[test.Base]; !exists {
			return errors.New("runtime corpus is invalid")
		}
		if _, duplicate := contractCases[test.Name]; duplicate {
			return errors.New("runtime corpus is invalid")
		}
		contractCases[test.Name] = struct{}{}
		for _, mutation := range test.Mutations {
			if mutation.Pointer == "" || mutation.Pointer[0] != '/' {
				return errors.New("runtime corpus is invalid")
			}
			switch mutation.Operation {
			case runtimeContractMutationSet, runtimeContractMutationAppend:
				if !json.Valid([]byte(mutation.ValueJSON)) {
					return errors.New("runtime corpus is invalid")
				}
			case runtimeContractMutationRemove:
				if mutation.ValueJSON != "" {
					return errors.New("runtime corpus is invalid")
				}
			default:
				return errors.New("runtime corpus is invalid")
			}
		}
		if test.Expected.Code == "" || test.Expected.Reason == "" {
			return errors.New("runtime corpus is invalid")
		}
	}
	corpus := RuntimeCorpus{document: document}
	_, err = corpus.ExpectedAdapterResult()
	return err
}

func runtimeCorpusCovers[T comparable](actual map[T]struct{}, required []T) bool {
	for _, value := range required {
		if _, exists := actual[value]; !exists {
			return false
		}
	}
	return true
}

func runtimeCorpusRosterMatches[T any](rows []T, expected []string, name func(T) string) bool {
	if len(rows) != len(expected) {
		return false
	}
	for index, row := range rows {
		if name(row) != expected[index] {
			return false
		}
	}
	return true
}

func validRuntimeAdapterEndpoint(value string) bool {
	parsed, err := url.Parse(value)
	if err != nil {
		return false
	}
	_, _, reason := normalizeEndpoint(parsed)
	return reason == ""
}

func cloneRuntimeCorpusDocument(input runtimeCorpusDocument) runtimeCorpusDocument {
	encoded, _ := json.Marshal(input)
	var result runtimeCorpusDocument
	_ = json.Unmarshal(encoded, &result)
	return result
}

func cloneRuntimeAdapterCases(input []RuntimeAdapterCase) []RuntimeAdapterCase {
	result := make([]RuntimeAdapterCase, len(input))
	for index, test := range input {
		result[index] = test
		if test.Credentials != nil {
			result[index].Credentials = append(make([]RuntimeCorpusCredentialValue, 0, len(test.Credentials)), test.Credentials...)
		}
		result[index].Transport.Headers = cloneRuntimeAdapterHeaders(test.Transport.Headers)
		if test.Expected.Request != nil {
			request := *test.Expected.Request
			request.Headers = cloneRuntimeAdapterHeaders(test.Expected.Request.Headers)
			if test.Expected.Request.Body != nil {
				body := *test.Expected.Request.Body
				request.Body = &body
			}
			result[index].Expected.Request = &request
		}
		if test.Expected.Outcome.Success != nil {
			success := *test.Expected.Outcome.Success
			result[index].Expected.Outcome.Success = &success
		}
		if test.Expected.Outcome.Error != nil {
			failure := *test.Expected.Outcome.Error
			result[index].Expected.Outcome.Error = &failure
		}
	}
	return result
}
