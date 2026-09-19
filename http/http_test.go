package http

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"go.osspkg.com/mcp"
)

func TestUnitHandlerSessionAndAuthorization(t *testing.T) {
	server, err := mcp.New(mcp.ServerInfo{Name: "test", Version: "1"}, mcp.WithMiddleware(func(next mcp.Handler) mcp.Handler {
		return func(ctx context.Context, request mcp.Request) (any, error) {
			if request.Meta.Headers["Authorization"] == "" {
				return nil, mcp.ErrUnauthorized
			}
			return next(ctx, request)
		}
	}))
	if err != nil {
		t.Fatal(err)
	}
	handler, err := NewHandler(server, DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"initialize"}`))
	request.Header.Set("Content-Type", "application/json")
	denied := httptest.NewRecorder()
	handler.ServeHTTP(denied, request)
	if denied.Code != http.StatusUnauthorized {
		t.Fatalf("got %d", denied.Code)
	}
	request = httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"initialize"}`))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer test")
	allowed := httptest.NewRecorder()
	handler.ServeHTTP(allowed, request)
	if allowed.Code != http.StatusOK || allowed.Header().Get("Mcp-Session-Id") == "" {
		t.Fatalf("status=%d headers=%v body=%s", allowed.Code, allowed.Header(), allowed.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(allowed.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
}

func TestUnitServerAcceptsNilContext(t *testing.T) {
	server := Server(nil, ServerConfig{})
	if server == nil {
		t.Fatal("nil server")
	}
	if err := server.Close(); err != nil {
		t.Fatal(err)
	}
}
