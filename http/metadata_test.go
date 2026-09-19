package http

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"go.osspkg.com/mcp"
)

func TestUnitMetadataHandler(t *testing.T) {
	handler, err := NewProtectedResourceMetadataHandler(mcp.ProtectedResourceMetadata{Resource: "https://api.example.test"})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, "/.well-known/oauth-protected-resource", nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"resource"`) {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	method := httptest.NewRequest(http.MethodPost, "/.well-known/oauth-protected-resource", nil)
	methodResponse := httptest.NewRecorder()
	handler.ServeHTTP(methodResponse, method)
	if methodResponse.Code != http.StatusMethodNotAllowed {
		t.Fatalf("method status=%d", methodResponse.Code)
	}
}
