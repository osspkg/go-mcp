package sse

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"go.osspkg.com/mcp"
)

const testServerName = "test"

func TestUnitSSEEndpointAndMessage(t *testing.T) {
	server, err := mcp.New(mcp.ServerInfo{Name: testServerName, Version: "1"})
	if err != nil {
		t.Fatal(err)
	}
	handler, err := NewHandler(server, DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, "/sse", nil)
	if err != nil {
		t.Fatal(err)
	}
	endpoint := httptest.NewRecorder()
	handler.ServeHTTP(endpoint, request)
	event := endpoint.Body.String()
	if !strings.Contains(event, "event: endpoint") {
		t.Fatalf("unexpected event: %q", event)
	}
	id, item, err := handler.newSession()
	if err != nil {
		t.Fatal(err)
	}
	post, err := http.NewRequest(http.MethodPost, "/message?sessionId="+id, strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"ping"}`))
	if err != nil {
		t.Fatal(err)
	}
	post.Header.Set("Content-Type", "application/json")
	posted := httptest.NewRecorder()
	handler.ServeHTTP(posted, post)
	if posted.Code != http.StatusAccepted {
		t.Fatalf("got status %d", posted.Code)
	}
	if response := <-item.messages; !strings.Contains(string(response), `"result":{}`) {
		t.Fatalf("unexpected message: %s", response)
	}
}

func TestUnitBackgroundSessionCleanup(t *testing.T) {
	server, err := mcp.New(mcp.ServerInfo{Name: testServerName, Version: "1"})
	if err != nil {
		t.Fatal(err)
	}
	handler, err := NewHandler(server, Config{
		SessionTTL:  20 * time.Millisecond,
		MaxSessions: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	id, item, err := handler.newSession()
	if err != nil {
		t.Fatal(err)
	}

	select {
	case <-item.done:
	case <-time.After(time.Second):
		t.Fatal("session was not expired by background cleanup")
	}

	handler.mu.Lock()
	_, exists := handler.sessions[id]
	running := handler.cleanupRunning
	handler.mu.Unlock()
	if exists {
		t.Fatal("expired session remains in the session map")
	}
	if running {
		t.Fatal("cleanup goroutine remains active without sessions")
	}
}

func TestUnitHandlerCloseStopsSessions(t *testing.T) {
	server, err := mcp.New(mcp.ServerInfo{Name: testServerName, Version: "1"})
	if err != nil {
		t.Fatal(err)
	}
	handler, err := NewHandler(server, DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	_, item, err := handler.newSession()
	if err != nil {
		t.Fatal(err)
	}
	handler.Close()
	select {
	case <-item.done:
	default:
		t.Fatal("Close did not terminate the session")
	}
	if _, _, err := handler.newSession(); err == nil {
		t.Fatal("new session accepted after Close")
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
