package client

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"go.osspkg.com/mcp"
	mcphttp "go.osspkg.com/mcp/http"
)

const (
	testProtocolVersion = "2025-11-25"
	jsonRPCField        = "jsonrpc"
	resultField         = "result"
)

type directTransport struct {
	receiver Receiver
	ready    chan struct{}
}

type recordingTransport struct {
	directTransport
	sent chan []byte
}

func (transport *directTransport) Ready() <-chan struct{} { return transport.ready }

func (transport *directTransport) Start(ctx context.Context, receiver Receiver) error {
	transport.receiver = receiver
	close(transport.ready)
	<-ctx.Done()
	return ctx.Err()
}

func (transport *directTransport) Send(_ context.Context, payload []byte) ([]byte, error) {
	var request struct {
		JSONRPC string          `json:"jsonrpc"`
		ID      json.RawMessage `json:"id"`
		Method  string          `json:"method"`
	}
	if err := json.Unmarshal(payload, &request); err != nil {
		return nil, err
	}
	if len(request.ID) == 0 {
		return nil, nil
	}
	return json.Marshal(map[string]any{jsonRPCField: rpcVersion, "id": request.ID, resultField: map[string]any{"ok": true}})
}

func (transport *directTransport) Close() error { return nil }

func (transport *recordingTransport) Send(ctx context.Context, payload []byte) ([]byte, error) {
	var envelope struct {
		Method string `json:"method"`
	}
	if err := json.Unmarshal(payload, &envelope); err != nil {
		return nil, err
	}
	if envelope.Method == "" {
		transport.sent <- append([]byte(nil), payload...)
		return nil, nil
	}
	return transport.directTransport.Send(ctx, payload)
}

func TestUnitClientCallAndInitialize(t *testing.T) {
	transport := &directTransport{ready: make(chan struct{})}
	client, err := NewWithConfig(transport, ClientConfig{Info: Implementation{Name: "test", Version: "1"}})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := client.Start(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := client.Initialize(ctx, InitializeParams{}); err != nil {
		t.Fatal(err)
	}
	var result map[string]any
	if err := client.Call(ctx, "custom", nil, &result); err != nil {
		t.Fatal(err)
	}
	ok, exists := result["ok"].(bool)
	if !exists || !ok {
		t.Fatalf("unexpected result: %#v", result)
	}
}

func TestUnitClientServerRequestHandler(t *testing.T) {
	transport := &directTransport{ready: make(chan struct{})}
	client, err := New(transport)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := client.Start(ctx); err != nil {
		t.Fatal(err)
	}
	called := make(chan bool, 1)
	if err := client.OnRequest("roots/list", func(_ context.Context, _ json.RawMessage) (any, error) {
		called <- true
		return map[string]any{"roots": []any{}}, nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := client.receive(ctx, []byte(`{"jsonrpc":"2.0","id":"server-1","method":"roots/list","params":{}}`)); err != nil {
		t.Fatal(err)
	}
	select {
	case <-called:
	case <-time.After(time.Second):
		t.Fatal("server request handler was not called")
	}
}

func TestUnitClientInboundHandlerLimit(t *testing.T) {
	transport := &recordingTransport{directTransport: directTransport{ready: make(chan struct{})}, sent: make(chan []byte, 1)}
	client, err := NewWithConfig(transport, ClientConfig{Info: Implementation{Name: "test", Version: "1"}, MaxConcurrentHandlers: 1})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := client.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = client.Close() }()
	started := make(chan struct{})
	var startedOnce sync.Once
	finish := make(chan struct{})
	if err := client.OnRequest("roots/list", func(context.Context, json.RawMessage) (any, error) {
		startedOnce.Do(func() { close(started) })
		<-finish
		return map[string]any{"roots": []any{}}, nil
	}); err != nil {
		t.Fatal(err)
	}
	request := func(id string) {
		t.Helper()
		if err := client.receive(ctx, []byte(`{"jsonrpc":"2.0","id":"`+id+`","method":"roots/list","params":{}}`)); err != nil {
			t.Fatal(err)
		}
	}
	request("one")
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("first handler did not start")
	}
	request("two")
	request("three")
	select {
	case payload := <-transport.sent:
		if !strings.Contains(string(payload), "handler capacity reached") {
			t.Fatalf("unexpected overload response: %s", payload)
		}
	case <-time.After(time.Second):
		t.Fatal("missing overload response")
	}
	close(finish)
}

func TestUnitHTTPTransportWithLibraryServer(t *testing.T) {
	server, err := mcp.New(mcp.ServerInfo{Name: "test-server", Version: "1"})
	if err != nil {
		t.Fatal(err)
	}
	handler, err := mcphttp.NewHandler(server, mcphttp.Config{Path: "/mcp"})
	if err != nil {
		t.Fatal(err)
	}
	defer handler.Close()
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Skipf("network listen unavailable: %v", err)
	}
	httpServer := httptest.NewUnstartedServer(handler)
	httpServer.Listener = listener
	httpServer.Start()
	defer httpServer.Close()
	transport, err := NewHTTPTransport(httpServer.URL+"/mcp", HTTPConfig{})
	if err != nil {
		t.Fatal(err)
	}
	client, err := New(transport)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := client.Start(ctx); err != nil {
		t.Fatal(err)
	}
	result, err := client.Initialize(ctx, InitializeParams{})
	if err != nil {
		t.Fatal(err)
	}
	if result.ProtocolVersion != testProtocolVersion {
		t.Fatalf("unexpected protocol version %q", result.ProtocolVersion)
	}
	if err := client.Ping(ctx); err != nil {
		t.Fatal(err)
	}
	if err := client.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestUnitRPCError(t *testing.T) {
	err := &RPCError{Code: -32601, Message: "missing"}
	if !errors.Is(err, err) || err.Error() == "" {
		t.Fatal("RPCError should implement error")
	}
}

func TestUnitHTTPTransportWithRoundTripper(t *testing.T) {
	var posts atomic.Int32
	httpClient := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		if request.Method == http.MethodGet {
			return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader("")), Header: make(http.Header)}, nil
		}
		posts.Add(1)
		payload, err := io.ReadAll(request.Body)
		if err != nil {
			return nil, err
		}
		var incoming struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
		}
		if err := json.Unmarshal(payload, &incoming); err != nil {
			return nil, err
		}
		body := bytes.NewBuffer(nil)
		if len(incoming.ID) > 0 {
			result := map[string]any{}
			if incoming.Method == "initialize" {
				result = map[string]any{"protocolVersion": testProtocolVersion, "capabilities": map[string]any{}, "serverInfo": map[string]any{"name": "mock", "version": "1"}}
			}
			response, err := json.Marshal(map[string]any{jsonRPCField: rpcVersion, "id": incoming.ID, resultField: result})
			if err != nil {
				return nil, err
			}
			_, _ = body.Write(response)
		}
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(body), Header: http.Header{"Mcp-Session-Id": []string{"mock-session"}}}, nil
	})}
	transport, err := NewHTTPTransport("http://mock.test/mcp", HTTPConfig{Client: httpClient, ReconnectDelay: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	client, err := New(transport)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := client.Start(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := client.Initialize(ctx, InitializeParams{}); err != nil {
		t.Fatal(err)
	}
	if err := client.Ping(ctx); err != nil {
		t.Fatal(err)
	}
	if posts.Load() < 3 {
		t.Fatalf("expected initialize, initialized, and ping posts; got %d", posts.Load())
	}
	_ = client.Close()
}

func TestUnitHTTPTransportReceivesSSEPostResponse(t *testing.T) {
	httpClient := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		payload, err := io.ReadAll(request.Body)
		if err != nil {
			return nil, err
		}
		var incoming struct {
			ID json.RawMessage `json:"id"`
		}
		if err := json.Unmarshal(payload, &incoming); err != nil {
			return nil, err
		}
		response, err := json.Marshal(map[string]any{jsonRPCField: rpcVersion, "id": incoming.ID, resultField: map[string]any{}})
		if err != nil {
			return nil, err
		}
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader("data: " + string(response) + "\n\n")), Header: http.Header{"Content-Type": []string{"text/event-stream"}}}, nil
	})}
	transport, err := NewHTTPTransport("http://mock.test/mcp", HTTPConfig{Client: httpClient})
	if err != nil {
		t.Fatal(err)
	}
	client, err := New(transport)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := client.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = client.Close() }()
	if err := client.Ping(ctx); err != nil {
		t.Fatalf("ping through SSE POST response: %v", err)
	}
}

func TestUnitSSETransportRejectsCrossOriginMessageEndpoint(t *testing.T) {
	httpClient := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader("event: endpoint\ndata: https://attacker.example/message\n\n")), Header: http.Header{"Content-Type": []string{"text/event-stream"}}}, nil
	})}
	transport, err := NewSSETransport("http://mock.test/sse", SSEConfig{Client: httpClient})
	if err != nil {
		t.Fatal(err)
	}
	err = transport.Start(context.Background(), func(context.Context, []byte) error { return nil })
	if err == nil || !strings.Contains(err.Error(), "must use the SSE endpoint origin") {
		t.Fatalf("expected cross-origin endpoint rejection, got %v", err)
	}
}

func TestUnitCommandTransport(t *testing.T) {
	env := append(os.Environ(), "GO_MCP_CLIENT_HELPER=1")
	transport, err := NewCommandTransport(os.Args[0], []string{"-test.run=TestClientHelperProcess", "--"}, CommandConfig{Env: env, Stderr: io.Discard})
	if err != nil {
		t.Fatal(err)
	}
	client, err := New(transport)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := client.Start(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := client.Initialize(ctx, InitializeParams{}); err != nil {
		t.Fatal(err)
	}
	if err := client.Ping(ctx); err != nil {
		t.Fatal(err)
	}
	if err := client.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestUnitCommandTransportStartError(t *testing.T) {
	transport, err := NewCommandTransport(filepath.Join(t.TempDir(), "missing-command"), nil, CommandConfig{Stderr: io.Discard})
	if err != nil {
		t.Fatal(err)
	}
	client, err := New(transport)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := client.Start(ctx); err == nil || !strings.Contains(err.Error(), "start command") {
		t.Fatalf("expected command startup error, got %v", err)
	}
}

func TestClientHelperProcess(t *testing.T) {
	if os.Getenv("GO_MCP_CLIENT_HELPER") != "1" {
		return
	}
	scanner := bufio.NewScanner(os.Stdin)
	writer := bufio.NewWriter(os.Stdout)
	for scanner.Scan() {
		var request struct {
			JSONRPC string          `json:"jsonrpc"`
			ID      json.RawMessage `json:"id"`
			Method  string          `json:"method"`
		}
		if err := json.Unmarshal(scanner.Bytes(), &request); err != nil || len(request.ID) == 0 {
			continue
		}
		result := map[string]any{}
		if request.Method == "initialize" {
			result = map[string]any{"protocolVersion": testProtocolVersion, "capabilities": map[string]any{}, "serverInfo": map[string]any{"name": "helper", "version": "1"}}
		}
		payload, err := json.Marshal(map[string]any{jsonRPCField: rpcVersion, "id": request.ID, resultField: result})
		if err != nil {
			os.Exit(2)
		}
		_, _ = writer.Write(append(payload, '\n'))
		if err := writer.Flush(); err != nil {
			os.Exit(2)
		}
	}
	os.Exit(0)
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (function roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return function(request)
}
