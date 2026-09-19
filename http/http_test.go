package http

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"go.osspkg.com/mcp"
)

const testServerName = "test"

func TestUnitHandlerSessionAndAuthorization(t *testing.T) {
	server, err := mcp.New(mcp.ServerInfo{Name: testServerName, Version: "1"}, mcp.WithMiddleware(func(next mcp.Handler) mcp.Handler {
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
	request.Header.Set("Content-Type", "application/json; charset=utf-8")
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

func TestUnitRejectsInvalidOrigin(t *testing.T) {
	server, err := mcp.New(mcp.ServerInfo{Name: testServerName, Version: "1"})
	if err != nil {
		t.Fatal(err)
	}
	handler, err := NewHandler(server, Config{AllowedOrigins: []string{"https://allowed.example"}})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"ping"}`))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Origin", "https://evil.example")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusForbidden {
		t.Fatalf("got status %d", response.Code)
	}
}

func TestUnitStreamableHTTPGetResumesNotifications(t *testing.T) {
	server, err := mcp.New(mcp.ServerInfo{Name: testServerName, Version: "1"})
	if err != nil {
		t.Fatal(err)
	}
	if err := server.RegisterResource(mcp.Resource{URI: "memo://one", Name: "one"}); err != nil {
		t.Fatal(err)
	}
	handler, err := NewHandler(server, DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	initRequest := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"initialize"}`))
	initRequest.Header.Set("Content-Type", "application/json")
	initResponse := httptest.NewRecorder()
	handler.ServeHTTP(initResponse, initRequest)
	sessionID := initResponse.Header().Get("Mcp-Session-Id")
	if sessionID == "" {
		t.Fatal("missing session id")
	}
	subscribe := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":2,"method":"resources/subscribe","params":{"uri":"memo://one"}}`))
	subscribe.Header.Set("Content-Type", "application/json")
	subscribe.Header.Set("Mcp-Session-Id", sessionID)
	handler.ServeHTTP(httptest.NewRecorder(), subscribe)
	if err := server.NotifyResourceUpdated(t.Context(), "", "memo://one"); err != nil {
		t.Fatal(err)
	}
	get := httptest.NewRequest(http.MethodGet, "/mcp", nil)
	get.Header.Set("Mcp-Session-Id", sessionID)
	stream := httptest.NewRecorder()
	handler.ServeHTTP(stream, get)
	if stream.Code != http.StatusOK || !strings.Contains(stream.Body.String(), "notifications/resources/updated") || !strings.Contains(stream.Body.String(), "id: "+sessionID+":") {
		t.Fatalf("status=%d body=%s", stream.Code, stream.Body.String())
	}
	lastID := strings.Split(strings.Split(stream.Body.String(), "id: ")[1], "\n")[0]
	resumed := httptest.NewRequest(http.MethodGet, "/mcp", nil)
	resumed.Header.Set("Mcp-Session-Id", sessionID)
	resumed.Header.Set("Last-Event-ID", lastID)
	resumedContext, cancel := context.WithTimeout(resumed.Context(), 10*time.Millisecond)
	defer cancel()
	resumed = resumed.WithContext(resumedContext)
	finished := make(chan struct{})
	go func() {
		handler.ServeHTTP(httptest.NewRecorder(), resumed)
		close(finished)
	}()
	select {
	case <-finished:
	case <-time.After(time.Second):
		t.Fatal("resumed GET did not stop on context cancellation")
	}
}

func TestUnitStreamableHTTPCorrelatesServerRequest(t *testing.T) {
	server, err := mcp.New(mcp.ServerInfo{Name: testServerName, Version: "1"}, mcp.WithMiddleware(func(next mcp.Handler) mcp.Handler {
		return func(ctx context.Context, request mcp.Request) (any, error) {
			if request.Method == "ping" {
				result, err := request.CallClient(ctx, "roots/list", map[string]any{})
				if err != nil {
					return nil, err
				}
				return result, nil
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
	initRequest := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"initialize"}`))
	initRequest.Header.Set("Content-Type", "application/json")
	initResponse := httptest.NewRecorder()
	handler.ServeHTTP(initResponse, initRequest)
	sessionID := initResponse.Header().Get("Mcp-Session-Id")
	if sessionID == "" {
		t.Fatal("missing session id")
	}
	result := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		ping := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":2,"method":"ping"}`))
		ping.Header.Set("Content-Type", "application/json")
		ping.Header.Set("Mcp-Session-Id", sessionID)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, ping)
		result <- response
	}()
	stream := httptest.NewRecorder()
	get := httptest.NewRequest(http.MethodGet, "/mcp", nil)
	get.Header.Set("Mcp-Session-Id", sessionID)
	handler.ServeHTTP(stream, get)
	data := strings.Split(stream.Body.String(), "data: ")
	if len(data) < 2 {
		t.Fatalf("stream=%s", stream.Body.String())
	}
	line := strings.TrimSpace(strings.Split(data[1], "\n")[0])
	var request struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal([]byte(line), &request); err != nil || request.ID == "" {
		t.Fatalf("request=%s err=%v", line, err)
	}
	response := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":"`+request.ID+`","result":{"roots":[]}}`))
	response.Header.Set("Content-Type", "application/json")
	response.Header.Set("Mcp-Session-Id", sessionID)
	handler.ServeHTTP(httptest.NewRecorder(), response)
	select {
	case pingResponse := <-result:
		if pingResponse.Code != http.StatusOK || !strings.Contains(pingResponse.Body.String(), `"roots"`) {
			t.Fatalf("ping=%d %s", pingResponse.Code, pingResponse.Body.String())
		}
	case <-time.After(time.Second):
		t.Fatal("server request was not completed")
	}
}
