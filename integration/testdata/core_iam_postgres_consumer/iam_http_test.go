package consumer_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"example.com/core-iam-consumer/coreapp"
)

type iamHTTPAdapter struct {
	service       *coreapp.IAMService
	authenticator *coreapp.AccessAuthenticator
	serviceCalls  *int
}

type tenantMemberWire struct {
	MemberID        string   `json:"memberId"`
	AccountID       string   `json:"accountId"`
	Username        string   `json:"username"`
	Email           string   `json:"email"`
	DisplayName     string   `json:"displayName"`
	SourceCode      string   `json:"sourceCode"`
	ExternalSubject string   `json:"externalSubject"`
	Status          string   `json:"status"`
	RoleCodes       []string `json:"roleCodes"`
	Version         uint64   `json:"version"`
}

type tenantMemberListWire struct {
	MemberIDs  []string           `json:"memberIds"`
	AccountIDs []string           `json:"accountIds"`
	Statuses   []string           `json:"statuses"`
	Versions   []uint64           `json:"versions"`
	Items      []tenantMemberWire `json:"items"`
	Total      uint64             `json:"total"`
}

func (a iamHTTPAdapter) ServeHTTP(response http.ResponseWriter, request *http.Request) {
	response.Header().Set("Content-Type", "application/json")
	const bearer = "Bearer "
	authorization := request.Header.Get("Authorization")
	if !strings.HasPrefix(authorization, bearer) {
		writeCoreHTTPError(response, coreapp.CodeInvalidCredentials)
		return
	}
	principal, err := a.authenticator.Authenticate(request.Context(), strings.TrimPrefix(authorization, bearer))
	if err != nil {
		writeCoreHTTPError(response, coreapp.CodeOf(err))
		return
	}
	if request.Method != http.MethodGet {
		response.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	tenantID := strings.TrimSpace(request.Header.Get("X-Tenant-ID"))
	if tenantID != "" && tenantID != principal.TenantID {
		writeCoreHTTPError(response, coreapp.CodePermissionDenied)
		return
	}

	if request.URL.Path == "/iam/members" {
		if !containsCode(principal.PermissionCodes, "read") {
			writeCoreHTTPError(response, coreapp.CodePermissionDenied)
			return
		}
		a.listMembers(response, request, principal.TenantID)
		return
	}
	const memberPrefix = "/iam/members/"
	if strings.HasPrefix(request.URL.Path, memberPrefix) {
		if !containsCode(principal.PermissionCodes, "read") {
			writeCoreHTTPError(response, coreapp.CodePermissionDenied)
			return
		}
		memberID := strings.TrimSpace(strings.TrimPrefix(request.URL.Path, memberPrefix))
		(*a.serviceCalls)++
		item, err := a.service.GetTenantMember(request.Context(), coreapp.TenantMemberKey{
			TenantID: principal.TenantID,
			MemberID: coreapp.TenantMemberID(memberID),
		})
		if err != nil {
			writeCoreHTTPError(response, coreapp.CodeOf(err))
			return
		}
		_ = json.NewEncoder(response).Encode(map[string]tenantMemberWire{"item": memberToWire(item)})
		return
	}
	http.NotFound(response, request)
}

func containsCode(values []string, expected string) bool {
	for _, value := range values {
		if value == expected {
			return true
		}
	}
	return false
}

func (a iamHTTPAdapter) listMembers(response http.ResponseWriter, request *http.Request, tenantID string) {
	query := request.URL.Query()
	limit, err := parseUint32(query, "limit")
	if err != nil {
		writeCoreHTTPError(response, coreapp.CodeInvalidInput)
		return
	}
	offset, err := parseUint32(query, "offset")
	if err != nil {
		writeCoreHTTPError(response, coreapp.CodeInvalidInput)
		return
	}
	status := coreapp.IAMStatus(query.Get("status"))
	if status != "" && status != coreapp.IAMStatusEnabled && status != coreapp.IAMStatusDisabled {
		writeCoreHTTPError(response, coreapp.CodeInvalidInput)
		return
	}
	if limit > 200 {
		writeCoreHTTPError(response, coreapp.CodeInvalidInput)
		return
	}
	(*a.serviceCalls)++
	page, err := a.service.ListTenantMembers(request.Context(), coreapp.ListTenantMembersInput{
		TenantID: tenantID,
		ListQuery: coreapp.ListQuery{
			Keyword: query.Get("keyword"),
			Status:  status,
			Limit:   limit,
			Offset:  offset,
		},
	})
	if err != nil {
		writeCoreHTTPError(response, coreapp.CodeOf(err))
		return
	}
	result := tenantMemberListWire{Total: page.Total, Items: make([]tenantMemberWire, len(page.Items))}
	for index, item := range page.Items {
		result.Items[index] = memberToWire(item)
		result.MemberIDs = append(result.MemberIDs, string(item.ID))
		result.AccountIDs = append(result.AccountIDs, string(item.AccountID))
		result.Statuses = append(result.Statuses, string(item.Status))
		result.Versions = append(result.Versions, item.Version)
	}
	_ = json.NewEncoder(response).Encode(result)
}

func memberToWire(item coreapp.TenantMember) tenantMemberWire {
	return tenantMemberWire{
		MemberID:        string(item.ID),
		AccountID:       string(item.AccountID),
		Username:        item.AccountUsername,
		Email:           item.AccountEmail,
		DisplayName:     item.AccountDisplayName,
		SourceCode:      item.AccountSourceCode,
		ExternalSubject: item.AccountExternalSubject,
		Status:          string(item.Status),
		RoleCodes:       append([]string(nil), item.ManualRoleCodes...),
		Version:         item.Version,
	}
}

func parseUint32(query url.Values, field string) (uint32, error) {
	value := strings.TrimSpace(query.Get(field))
	if value == "" {
		return 0, nil
	}
	parsed, err := strconv.ParseUint(value, 10, 32)
	if err != nil {
		return 0, err
	}
	return uint32(parsed), nil
}

func writeCoreHTTPError(response http.ResponseWriter, code coreapp.ErrorCode) {
	status := http.StatusInternalServerError
	switch code {
	case coreapp.CodeInvalidInput:
		status = http.StatusBadRequest
	case coreapp.CodeInvalidCredentials, coreapp.CodeSessionExpired, coreapp.CodeSessionReplayed:
		status = http.StatusUnauthorized
	case coreapp.CodePermissionDenied:
		status = http.StatusForbidden
	case coreapp.CodeNotFound:
		status = http.StatusNotFound
	case coreapp.CodeConflict, coreapp.CodeConcurrentWrite:
		status = http.StatusConflict
	case coreapp.CodeFailedPrecondition:
		status = http.StatusPreconditionFailed
	}
	response.WriteHeader(status)
	_ = json.NewEncoder(response).Encode(map[string]string{"code": string(code)})
}

func exerciseIAMHTTPTransport(t *testing.T, service *coreapp.IAMService, authenticator *coreapp.AccessAuthenticator, accessToken, deniedAccessToken, tenantID, otherTenantID string, memberID coreapp.TenantMemberID, accountID coreapp.IdentityAccountID) {
	t.Helper()
	serviceCalls := 0
	server := httptest.NewServer(iamHTTPAdapter{service: service, authenticator: authenticator, serviceCalls: &serviceCalls})
	t.Cleanup(server.Close)

	request := func(path, tenant, token string) *http.Response {
		t.Helper()
		req, err := http.NewRequest(http.MethodGet, server.URL+path, nil)
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("X-Tenant-ID", tenant)
		response, err := server.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		return response
	}

	response := request("/iam/members?keyword=Bootstrap&status=enabled&limit=10&offset=0", tenantID, accessToken)
	if response.StatusCode != http.StatusOK || response.Header.Get("Content-Type") != "application/json" {
		t.Fatalf("member list status=%d content-type=%q", response.StatusCode, response.Header.Get("Content-Type"))
	}
	var page tenantMemberListWire
	if err := json.NewDecoder(response.Body).Decode(&page); err != nil {
		response.Body.Close()
		t.Fatal(err)
	}
	response.Body.Close()
	if page.Total != 1 || len(page.Items) != 1 || page.Items[0].MemberID != string(memberID) || page.Items[0].AccountID != string(accountID) || page.Items[0].Username != "owner" || page.Items[0].DisplayName != "Bootstrap Owner" || len(page.Items[0].RoleCodes) != 1 || page.Items[0].RoleCodes[0] != "operator" {
		t.Fatalf("member HTTP page=%#v", page)
	}
	response = request("/iam/members", "", accessToken)
	if response.StatusCode != http.StatusOK {
		response.Body.Close()
		t.Fatalf("principal tenant fallback status=%d", response.StatusCode)
	}
	response.Body.Close()

	beforeRejected := serviceCalls
	response = request("/iam/members", otherTenantID, accessToken)
	if response.StatusCode != http.StatusForbidden {
		response.Body.Close()
		t.Fatalf("other tenant list status=%d", response.StatusCode)
	}
	response.Body.Close()
	if serviceCalls != beforeRejected {
		t.Fatal("cross-tenant request called service")
	}
	response = request("/iam/members", otherTenantID, deniedAccessToken)
	if response.StatusCode != http.StatusForbidden {
		response.Body.Close()
		t.Fatalf("missing permission status=%d", response.StatusCode)
	}
	response.Body.Close()
	if serviceCalls != beforeRejected {
		t.Fatal("permission-denied request called service")
	}

	response = request("/iam/members/"+url.PathEscape(string(memberID)), tenantID, accessToken)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("member get status=%d", response.StatusCode)
	}
	var itemResponse struct {
		Item tenantMemberWire `json:"item"`
	}
	if err := json.NewDecoder(response.Body).Decode(&itemResponse); err != nil {
		response.Body.Close()
		t.Fatal(err)
	}
	response.Body.Close()
	if itemResponse.Item.MemberID != string(memberID) || itemResponse.Item.AccountID != string(accountID) {
		t.Fatalf("member HTTP get=%#v", itemResponse)
	}

	for _, test := range []struct {
		name       string
		path       string
		tenant     string
		token      string
		wantStatus int
		wantCode   string
	}{
		{name: "invalid-status", path: "/iam/members?status=unknown", tenant: tenantID, token: accessToken, wantStatus: http.StatusBadRequest, wantCode: "invalid_input"},
		{name: "limit-overflow", path: "/iam/members?limit=201", tenant: tenantID, token: accessToken, wantStatus: http.StatusBadRequest, wantCode: "invalid_input"},
		{name: "cross-tenant-get", path: "/iam/members/" + url.PathEscape(string(memberID)), tenant: otherTenantID, token: accessToken, wantStatus: http.StatusForbidden, wantCode: "permission_denied"},
		{name: "invalid-token", path: "/iam/members", tenant: tenantID, token: "wrong", wantStatus: http.StatusUnauthorized, wantCode: "invalid_credentials"},
	} {
		t.Run("http-"+test.name, func(t *testing.T) {
			before := serviceCalls
			response := request(test.path, test.tenant, test.token)
			defer response.Body.Close()
			var body map[string]string
			if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
				t.Fatal(err)
			}
			if response.StatusCode != test.wantStatus || body["code"] != test.wantCode {
				t.Fatalf("status=%d body=%v, want %d %s", response.StatusCode, body, test.wantStatus, test.wantCode)
			}
			if serviceCalls != before {
				t.Fatalf("rejected request called service: before=%d after=%d", before, serviceCalls)
			}
		})
	}
	if serviceCalls != 3 {
		t.Fatalf("service calls=%d want=3", serviceCalls)
	}

	if page.MemberIDs[0] != string(memberID) || page.AccountIDs[0] != string(accountID) || page.Statuses[0] != "enabled" || page.Versions[0] == 0 {
		t.Fatal(fmt.Sprintf("projected response fields=%#v", page))
	}
}
