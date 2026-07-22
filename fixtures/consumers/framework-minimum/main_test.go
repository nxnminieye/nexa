package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestFrameworkMinimumHasNoOptionalCompositionAndReportsHealth(t *testing.T) {
	composed, handler, err := newMinimumApplication()
	if err != nil {
		t.Fatal(err)
	}
	inspection := composed.Inspect()
	if len(inspection.Plugins) != 0 || len(inspection.Capabilities) != 0 {
		t.Fatalf("minimum composition = plugins %#v capabilities %#v", inspection.Plugins, inspection.Capabilities)
	}

	request := httptest.NewRequest(http.MethodGet, "/health", nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("health status = %d", response.Code)
	}
	var health struct {
		Ready bool `json:"ready"`
	}
	if err := json.NewDecoder(response.Body).Decode(&health); err != nil {
		t.Fatal(err)
	}
	if !health.Ready {
		t.Fatal("minimum health is not ready")
	}
}
