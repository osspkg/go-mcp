package sse

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"go.osspkg.com/mcp"
)

func TestUnitSSEEndpointAndMessage(t *testing.T) {
	server, err := mcp.New(mcp.ServerInfo{Name: "test", Version: "1"})
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
