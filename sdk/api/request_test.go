package api

import (
	"bytes"
	"encoding/json"
	"errors"
	"math/rand"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/gowebpki/jcs"
	"github.com/nxnminieye/nexa/cli/protocol"
)

func TestParseRequestRequiresRootObject(t *testing.T) {
	_, err := ParseRequest([]byte(`[1,2,3]`))
	requireRequestFailure(t, err, "root_object_required", "")
}

func TestParseRequestRejectsDuplicateKeyWithPointer(t *testing.T) {
	_, err := ParseRequest([]byte(`{"outer":{"a/b":1,"a/b":2}}`))
	requireRequestFailure(t, err, "duplicate_key", "/outer/a~1b")
}

func TestParseRequestRejectsTrailingInput(t *testing.T) {
	_, err := ParseRequest([]byte(`{} {}`))
	requireRequestFailure(t, err, "trailing_input", "")
}

func TestParseRequestRejectsInvalidUTF8(t *testing.T) {
	data := append([]byte(`{"value":"`), 0xff)
	data = append(data, []byte(`"}`)...)
	_, err := ParseRequest(data)
	requireRequestFailure(t, err, "invalid_utf8", "/value")
}

func TestParseRequestRejectsUnpairedSurrogate(t *testing.T) {
	_, err := ParseRequest([]byte(`{"value":"\ud800"}`))
	requireRequestFailure(t, err, "invalid_unicode_scalar", "/value")
}

func TestParseRequestRejectsNestedNull(t *testing.T) {
	_, err := ParseRequest([]byte(`{"outer":[true,{"value":null}]}`))
	requireRequestFailure(t, err, "null_not_allowed", "/outer/1/value")
}

func TestParseRequestEnforcesDepthLimit(t *testing.T) {
	const depth = 65
	data := []byte(`{"value":` + strings.Repeat(`[`, depth) + `true` + strings.Repeat(`]`, depth) + `}`)
	_, err := ParseRequest(data)
	requireRequestFailure(t, err, "depth_limit_exceeded", "/value"+strings.Repeat("/0", 64))
}

func TestParseRequestEnforcesSizeLimit(t *testing.T) {
	data := []byte(`{"value":"` + strings.Repeat("a", 1<<20) + `"}`)
	_, err := ParseRequest(data)
	requireRequestFailure(t, err, "size_limit_exceeded", "")
}

func TestParseRequestEnforcesNodeLimit(t *testing.T) {
	data := []byte(`{"values":[` + strings.Repeat(`0,`, 70_000) + `0]}`)
	_, err := ParseRequest(data)
	requireRequestFailure(t, err, "node_limit_exceeded", "/values/65534")
}

func TestRuntimeLimitsAreVersionedImmutableAndDriveRequestParsing(t *testing.T) {
	want := RuntimeLimitSet{
		APIVersion:       RuntimeLimitsAPIVersion,
		RequestRawBytes:  1 << 20,
		JSONDepth:        64,
		JSONNodes:        65_536,
		ResponseBytesMin: 1,
		ResponseBytesMax: 64 << 20,
	}
	if RuntimeLimitsAPIVersion != "nexa.dev/runtime-api-limits/v1" {
		t.Fatalf("RuntimeLimitsAPIVersion = %q", RuntimeLimitsAPIVersion)
	}
	limits := RuntimeLimits()
	if limits != want {
		t.Fatalf("RuntimeLimits() = %#v, want %#v", limits, want)
	}
	limits.RequestRawBytes = 1
	if got := RuntimeLimits(); got != want {
		t.Fatalf("limit mutation leaked: %#v", got)
	}

	exact := []byte(`{"value":"` + strings.Repeat("a", want.RequestRawBytes-len(`{"value":""}`)) + `"}`)
	if len(exact) != want.RequestRawBytes {
		t.Fatalf("exact request bytes = %d, want %d", len(exact), want.RequestRawBytes)
	}
	if _, err := ParseRequest(exact); err != nil {
		t.Fatalf("request at raw-byte limit rejected: %v", err)
	}
	_, err := ParseRequest(append(exact, ' '))
	requireRequestFailure(t, err, "size_limit_exceeded", "")

	exactDepth := []byte(`{"value":` + strings.Repeat("[", want.JSONDepth-1) + `true` + strings.Repeat("]", want.JSONDepth-1) + `}`)
	if _, err := ParseRequest(exactDepth); err != nil {
		t.Fatalf("request at depth limit rejected: %v", err)
	}
	overDepth := []byte(`{"value":` + strings.Repeat("[", want.JSONDepth) + `true` + strings.Repeat("]", want.JSONDepth) + `}`)
	_, err = ParseRequest(overDepth)
	requireRequestFailure(t, err, "depth_limit_exceeded", "/value"+strings.Repeat("/0", want.JSONDepth))

	exactNodes := requestWithArrayElements(want.JSONNodes - 2)
	if _, err := ParseRequest(exactNodes); err != nil {
		t.Fatalf("request at node limit rejected: %v", err)
	}
	overNodes := requestWithArrayElements(want.JSONNodes - 1)
	_, err = ParseRequest(overNodes)
	requireRequestFailure(t, err, "node_limit_exceeded", "/values/"+strconv.Itoa(want.JSONNodes-2))
}

func TestRuntimeBoundarySemanticsAreClosedImmutableAndHaveZeroBehavior(t *testing.T) {
	limits := RuntimeLimits()
	request := limits.RequestRawBytesSemantics()
	if request.Scope() != RuntimeBoundaryScopeParseRequest || !request.FirstFailure() {
		t.Fatalf("request raw-byte semantics = scope %q first %t", request.Scope(), request.FirstFailure())
	}
	response := limits.ResponseBytesSemantics()
	if response.Scope() != RuntimeBoundaryScopeClientCall || !response.BeforeRemoteErrorParse() {
		t.Fatalf("response-byte semantics = scope %q before remote %t", response.Scope(), response.BeforeRemoteErrorParse())
	}

	request = RequestRawBytesSemantics{}
	response = ResponseBytesSemantics{}
	if got := RuntimeLimits().RequestRawBytesSemantics(); got.Scope() != RuntimeBoundaryScopeParseRequest || !got.FirstFailure() {
		t.Fatalf("request semantics mutation escaped: %#v", got)
	}
	if got := RuntimeLimits().ResponseBytesSemantics(); got.Scope() != RuntimeBoundaryScopeClientCall || !got.BeforeRemoteErrorParse() {
		t.Fatalf("response semantics mutation escaped: %#v", got)
	}
	if got := (RuntimeLimitSet{}).RequestRawBytesSemantics(); got.Scope() != "" || got.FirstFailure() {
		t.Fatalf("zero request semantics = %#v", got)
	}
	if got := (RuntimeLimitSet{}).ResponseBytesSemantics(); got.Scope() != "" || got.BeforeRemoteErrorParse() {
		t.Fatalf("zero response semantics = %#v", got)
	}
}

func TestParseRequestRawByteLimitHasFirstFailurePrecedence(t *testing.T) {
	limits := RuntimeLimits()
	semantics := limits.RequestRawBytesSemantics()
	if semantics.Scope() != RuntimeBoundaryScopeParseRequest || !semantics.FirstFailure() {
		t.Fatalf("raw-byte owner does not declare first-failure ParseRequest behavior: %#v", semantics)
	}
	_, err := ParseRequest(bytes.Repeat([]byte{'{'}, limits.RequestRawBytes+1))
	requireRequestFailure(t, err, "size_limit_exceeded", "")
}

func TestParseRequestNormalizesObjectOrderAndPreservesIntegerLexemes(t *testing.T) {
	first, err := ParseRequest([]byte(`{"z":9007199254740993123456789,"a":{"y":1e2,"x":-0}}`))
	if err != nil {
		t.Fatal(err)
	}
	second, err := ParseRequest([]byte(`{"a":{"x":-0,"y":1e2},"z":9007199254740993123456789}`))
	if err != nil {
		t.Fatal(err)
	}
	want := []byte(`{"a":{"x":-0,"y":1e2},"z":9007199254740993123456789}`)
	if got := first.JSON(); !bytes.Equal(got, want) {
		t.Fatalf("normalized request = %s, want %s", got, want)
	}
	if !bytes.Equal(first.JSON(), second.JSON()) {
		t.Fatalf("member order changed normalized bytes: %s != %s", first.JSON(), second.JSON())
	}
}

func TestParseRequestUTF16MemberOrderMatchesJCS(t *testing.T) {
	assertRequestMatchesJCS(t, map[string]any{
		"ordinary":       1,
		"\x00control":    2,
		"\u007fboundary": 3,
		"\ud7ffbmp":      4,
		"\ue000bmp":      5,
		"\uffffbmp":      6,
		"\U00010000pair": 7,
		"\U0001f642pair": 8,
	})

	rng := rand.New(rand.NewSource(418042))
	alphabet := []rune{
		0, 0x1f, '"', '\\', 'a', 'Z', '~', 0x7f, 0x80, 0x7ff, 0x800,
		0xd7ff, 0xe000, 0xffff, 0x10000, 0x1f642, 0x10ffff,
	}
	for iteration := 0; iteration < 300; iteration++ {
		object := make(map[string]any, 12)
		for len(object) < 12 {
			length := 1 + rng.Intn(6)
			key := make([]rune, length)
			for index := range key {
				key[index] = alphabet[rng.Intn(len(alphabet))]
			}
			object[string(key)] = len(object)
		}
		assertRequestMatchesJCS(t, object)
	}
}

func assertRequestMatchesJCS(t *testing.T, object map[string]any) {
	t.Helper()
	input, err := json.Marshal(object)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	want, err := jcs.Transform(input)
	if err != nil {
		t.Fatalf("jcs.Transform() error = %v", err)
	}
	request, err := ParseRequest(input)
	if err != nil {
		t.Fatalf("ParseRequest() error = %v", err)
	}
	if got := request.JSON(); !bytes.Equal(got, want) {
		t.Fatalf("ParseRequest() UTF-16 member order differs from JCS\n got: %q\nwant: %q", got, want)
	}
}

func TestParseRequestJSONReturnsDefensiveCopies(t *testing.T) {
	request, err := ParseRequest([]byte(`{"value":"original"}`))
	if err != nil {
		t.Fatal(err)
	}
	first := request.JSON()
	first[2] = 'X'
	if got := string(request.JSON()); got != `{"value":"original"}` {
		t.Fatalf("JSON mutation leaked into request: %s", got)
	}
}

func TestSDKErrorCodesAreFrozen(t *testing.T) {
	got := []string{
		codeClientInvalid,
		codeAPIManifestRequired,
		codeOperationNotFound,
		codeRequestInvalid,
		codeCredentialProviderError,
		codeTransportError,
		codeRemoteProtocolError,
		codeRemoteErrorUnmapped,
		codeOperationCanceled,
	}
	want := []string{
		"client_invalid",
		"api_manifest_required",
		"operation_not_found",
		"request_invalid",
		"credential_provider_error",
		"transport_error",
		"remote_protocol_error",
		"remote_error_unmapped",
		"operation_canceled",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("SDK error codes = %v, want %v", got, want)
	}
}

func requestWithArrayElements(count int) []byte {
	if count == 0 {
		return []byte(`{"values":[]}`)
	}
	values := strings.TrimSuffix(strings.Repeat("0,", count), ",")
	return []byte(`{"values":[` + values + `]}`)
}

func TestCredentialProviderErrorIdentityIsSafeAndStable(t *testing.T) {
	apiError := newSDKError(
		codeCredentialProviderError,
		sdkErrorDomain,
		protocol.CategoryExternal,
		credentialProviderFailureMessage,
		ErrorDetails{reason: credentialProviderFailureReason},
	)
	if apiError.Code() != "credential_provider_error" || apiError.Domain() != "nexa.sdk.api" || apiError.Category() != protocol.CategoryExternal || apiError.Retryable() {
		t.Fatalf("error identity = (%q, %q, %q, %t)", apiError.Code(), apiError.Domain(), apiError.Category(), apiError.Retryable())
	}
	if apiError.Error() != "credential provider failed" || apiError.Details().Reason() != "provider_failed" || apiError.Details().Pointer() != "" {
		t.Fatalf("error projection = (%q, %q, %q)", apiError.Error(), apiError.Details().Reason(), apiError.Details().Pointer())
	}
	if apiError.Unwrap() == nil || errors.Unwrap(apiError.Unwrap()) != nil {
		t.Fatalf("credential provider error must expose only the stable SDK sentinel: %v", apiError.Unwrap())
	}
}

func requireRequestFailure(t *testing.T, err error, reason, pointer string) {
	t.Helper()
	if err == nil {
		t.Fatalf("ParseRequest succeeded, want %s at %q", reason, pointer)
	}
	var apiError *Error
	if !errors.As(err, &apiError) {
		t.Fatalf("error = %T %v, want *api.Error", err, err)
	}
	if apiError.Code() != "request_invalid" || apiError.Domain() != "nexa.sdk.api" || apiError.Category() != protocol.CategoryInput || apiError.Retryable() {
		t.Fatalf("error identity = (%q, %q, %q, %t)", apiError.Code(), apiError.Domain(), apiError.Category(), apiError.Retryable())
	}
	if apiError.Unwrap() == nil || apiError.APIOperationID() != "" || apiError.RequestID() != "" || apiError.TraceID() != "" {
		t.Fatalf("unexpected request error context: %#v", apiError)
	}
	detail := apiError.Details()
	gotReason := detail.Reason()
	gotPointer := detail.Pointer()
	if gotReason != reason || gotPointer != pointer {
		t.Fatalf("failure = (%q, %q), want (%q, %q)", gotReason, gotPointer, reason, pointer)
	}
}
