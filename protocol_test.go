package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

type testInput struct {
	Name string `json:"name" mcp:"description=person name"`
}

func (input *testInput) UnmarshalJSON(data []byte) error {
	type plain testInput
	return json.Unmarshal(data, (*plain)(input))
}

type testOutput struct {
	Greeting string `json:"greeting"`
}

func (output *testOutput) MarshalJSON() ([]byte, error) {
	type plain testOutput
	return json.Marshal((*plain)(output))
}

type recursiveInput struct {
	Next *recursiveInput `json:"next,omitempty"`
}

const pingMethod = "ping"

const testServerName = "test"

func (input *recursiveInput) UnmarshalJSON(data []byte) error {
	type plain recursiveInput
	return json.Unmarshal(data, (*plain)(input))
}

func TestUnitRegisterToolAndCall(t *testing.T) {
	server, err := New(ServerInfo{Name: testServerName, Version: "1"})
	if err != nil {
		t.Fatal(err)
	}
	err = RegisterTool(server, "hello", "Greets a person", func(_ context.Context, input *testInput) (*testOutput, error) {
		return &testOutput{Greeting: "hello " + input.Name}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	response, err := server.ServeJSON(t.Context(), []byte(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"hello","arguments":{"name":"Ada"}}}`), RequestMeta{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(response), `hello Ada`) || !strings.Contains(string(response), `structuredContent`) {
		t.Fatalf("unexpected response: %s", response)
	}
	list, err := server.ServeJSON(t.Context(), []byte(`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`), RequestMeta{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(list), `"required":["name"]`) || !strings.Contains(string(list), "person name") {
		t.Fatalf("unexpected schema: %s", list)
	}
}

func TestUnitHandlerPanicBecomesProtocolError(t *testing.T) {
	server, err := New(ServerInfo{Name: testServerName, Version: "1"})
	if err != nil {
		t.Fatal(err)
	}
	if err := RegisterTool(server, "panic", "", func(context.Context, *testInput) (*testOutput, error) {
		panic("boom")
	}); err != nil {
		t.Fatal(err)
	}
	response, err := server.ServeJSON(t.Context(), []byte(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"panic","arguments":{}}}`), RequestMeta{})
	if err != nil || !strings.Contains(string(response), `"code":-32603`) {
		t.Fatalf("response=%s err=%v", response, err)
	}
}

func TestUnitMiddlewareAuthorization(t *testing.T) {
	server, err := New(ServerInfo{Name: testServerName, Version: "1"}, WithMiddleware(func(next Handler) Handler {
		return func(ctx context.Context, request Request) (any, error) {
			if request.Meta.Headers["Authorization"] == "" {
				return nil, ErrUnauthorized
			}
			return next(ctx, request)
		}
	}))
	if err != nil {
		t.Fatal(err)
	}
	response, err := server.ServeJSON(t.Context(), []byte(`{"jsonrpc":"2.0","id":1,"method":"ping"}`), RequestMeta{Headers: map[string]string{}})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(response), `"code":-32001`) {
		t.Fatalf("unexpected response: %s", response)
	}
}

func TestUnitRequestObserver(t *testing.T) {
	observed := make(chan Request, 1)
	server, err := New(ServerInfo{Name: testServerName, Version: "1"}, WithRequestObserver(func(_ context.Context, request Request, duration time.Duration) {
		if duration < 0 {
			t.Fatal("negative duration")
		}
		observed <- request
	}))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := server.ServeJSON(t.Context(), []byte(`{"jsonrpc":"2.0","id":1,"method":"ping"}`), RequestMeta{}); err != nil {
		t.Fatal(err)
	}
	request := <-observed
	if request.Method != pingMethod {
		t.Fatalf("method = %q", request.Method)
	}
}

func TestUnitResourcesAndPrompts(t *testing.T) {
	server, _ := New(ServerInfo{Name: testServerName, Version: "1"})
	if err := server.RegisterResource(Resource{URI: "memo://one", Name: "one", Text: "content"}); err != nil {
		t.Fatal(err)
	}
	if err := server.RegisterPrompt(Prompt{Name: "welcome", Messages: []PromptMessage{{Role: "user", Text: "hello"}}}); err != nil {
		t.Fatal(err)
	}
	for _, request := range []string{`{"jsonrpc":"2.0","id":1,"method":"resources/read","params":{"uri":"memo://one"}}`, `{"jsonrpc":"2.0","id":2,"method":"prompts/get","params":{"name":"welcome"}}`} {
		response, err := server.ServeJSON(t.Context(), []byte(request), RequestMeta{})
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(response), `"error"`) {
			t.Fatalf("unexpected response: %s", response)
		}
	}
}

func TestUnitRejectsRegistrationAfterRequest(t *testing.T) {
	server, _ := New(ServerInfo{Name: testServerName, Version: "1"})
	_, _ = server.ServeJSON(t.Context(), []byte(`{"jsonrpc":"2.0","id":1,"method":"ping"}`), RequestMeta{})
	err := server.RegisterResource(Resource{URI: "memo://one", Name: "one", Text: "content"})
	if !errors.Is(err, ErrStarted) {
		t.Fatalf("got %v, want ErrStarted", err)
	}
}

func TestUnitRegisterToolRejectsRecursiveInput(t *testing.T) {
	server, err := New(ServerInfo{Name: testServerName, Version: "1"})
	if err != nil {
		t.Fatal(err)
	}
	err = RegisterTool(server, "recursive", "", func(_ context.Context, _ *recursiveInput) (*testOutput, error) {
		return &testOutput{}, nil
	})
	if err == nil || !strings.Contains(err.Error(), "recursive Go type") {
		t.Fatalf("got %v, want recursive type error", err)
	}
}

func TestUnitRegisterPromptCopiesSlices(t *testing.T) {
	const changed = "changed"
	server, err := New(ServerInfo{Name: testServerName, Version: "1"})
	if err != nil {
		t.Fatal(err)
	}
	arguments := []PromptArgument{{Name: "name"}}
	messages := []PromptMessage{{Role: "user", Text: "original"}}
	if err := server.RegisterPrompt(Prompt{Name: "welcome", Arguments: arguments, Messages: messages}); err != nil {
		t.Fatal(err)
	}
	arguments[0].Name = changed
	messages[0].Text = changed

	response, err := server.ServeJSON(t.Context(), []byte(`{"jsonrpc":"2.0","id":1,"method":"prompts/get","params":{"name":"welcome"}}`), RequestMeta{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(response), "original") || strings.Contains(string(response), "changed") {
		t.Fatalf("prompt changed after registration: %s", response)
	}
	listing, err := server.ServeJSON(t.Context(), []byte(`{"jsonrpc":"2.0","id":2,"method":"prompts/list"}`), RequestMeta{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(listing), `"name":"name"`) || strings.Contains(string(listing), `"name":"changed"`) {
		t.Fatalf("prompt arguments changed after registration: %s", listing)
	}

	done := make(chan struct{})
	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		for {
			select {
			case <-done:
				return
			default:
				arguments[0].Name = changed
				messages[0].Text = changed
			}
		}
	}()
	for range 100 {
		if _, err := server.ServeJSON(t.Context(), []byte(`{"jsonrpc":"2.0","id":3,"method":"prompts/list"}`), RequestMeta{}); err != nil {
			close(done)
			<-stopped
			t.Fatal(err)
		}
	}
	close(done)
	<-stopped
}

func TestUnitRejectsInvalidJSONRPCIDAndParams(t *testing.T) {
	server, err := New(ServerInfo{Name: testServerName, Version: "1"})
	if err != nil {
		t.Fatal(err)
	}
	for _, payload := range []string{
		`{"jsonrpc":"2.0","id":{},"method":"ping"}`,
		`{"jsonrpc":"2.0","id":1,"method":"ping","params":[]}`,
	} {
		response, err := server.ServeJSON(t.Context(), []byte(payload), RequestMeta{})
		if err != nil || !strings.Contains(string(response), `"code":-32600`) {
			t.Fatalf("payload %s: response=%s err=%v", payload, response, err)
		}
	}
}

func TestUnitResourceTemplateDecodesSafeVariable(t *testing.T) {
	values, ok := matchTemplate("memo:///{name}", "memo:///Ada%20Lovelace")
	if !ok || values["name"] != "Ada Lovelace" {
		t.Fatalf("values=%v ok=%v", values, ok)
	}
	if _, ok := matchTemplate("memo:///{name}", "memo:///%2F"); ok {
		t.Fatal("accepted escaped path separator")
	}
}

func TestUnitTaskAugmentationAndPolling(t *testing.T) {
	server, err := New(ServerInfo{Name: testServerName, Version: "1"})
	if err != nil {
		t.Fatal(err)
	}
	if err := RegisterTool(server, "task", "", func(_ context.Context, input *testInput) (*testOutput, error) {
		return &testOutput{Greeting: input.Name}, nil
	}); err != nil {
		t.Fatal(err)
	}
	response, err := server.ServeJSON(t.Context(), []byte(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"task","task":{"ttl":1000},"arguments":{"name":"done"}}}`), RequestMeta{})
	if err != nil {
		t.Fatal(err)
	}
	var envelope struct {
		Result struct {
			Task Task `json:"task"`
		} `json:"result"`
	}
	if err := json.Unmarshal(response, &envelope); err != nil || envelope.Result.Task.TaskID == "" {
		t.Fatalf("response=%s err=%v", response, err)
	}
	result, err := server.GetTaskResult(t.Context(), envelope.Result.Task.TaskID)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(mustJSON(t, result)), "done") {
		t.Fatalf("result=%v", result)
	}
	polled, err := server.ServeJSON(t.Context(), []byte(`{"jsonrpc":"2.0","id":2,"method":"tasks/result","params":{"taskId":"`+envelope.Result.Task.TaskID+`"}}`), RequestMeta{})
	if err != nil || strings.Contains(string(polled), `"error"`) {
		t.Fatalf("polled=%s err=%v", polled, err)
	}
}

func TestUnitRequestClientFeatureHelpers(t *testing.T) {
	server, err := New(ServerInfo{Name: testServerName, Version: "1"}, WithMiddleware(func(next Handler) Handler {
		return func(ctx context.Context, request Request) (any, error) {
			if request.Method == pingMethod {
				roots, err := request.ListRoots(ctx)
				if err != nil || len(roots.Roots) != 1 {
					return nil, err
				}
			}
			return next(ctx, request)
		}
	}))
	if err != nil {
		t.Fatal(err)
	}
	response, err := server.ServeJSON(t.Context(), []byte(`{"jsonrpc":"2.0","id":1,"method":"ping"}`), RequestMeta{Call: func(_ context.Context, method string, _ any) (json.RawMessage, error) {
		if method != "roots/list" {
			t.Fatalf("method=%s", method)
		}
		return json.RawMessage(`{"roots":[{"uri":"file:///tmp"}]}`), nil
	}})
	if err != nil || !strings.Contains(string(response), `"result"`) {
		t.Fatalf("response=%s err=%v", response, err)
	}
}

func mustJSON(t *testing.T, value any) []byte {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return encoded
}

type testTransport func(context.Context, *Server) error

func (transport testTransport) Serve(ctx context.Context, server *Server) error {
	return transport(ctx, server)
}

func TestUnitRunCancelsTransports(t *testing.T) {
	server, _ := New(ServerInfo{Name: testServerName, Version: "1"})
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Millisecond)
	defer cancel()
	err := server.RunContext(ctx, testTransport(func(ctx context.Context, _ *Server) error { <-ctx.Done(); return ctx.Err() }))
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("got %v", err)
	}
}

func TestUnitRunWaitsForTransportShutdownAfterFailure(t *testing.T) {
	server, _ := New(ServerInfo{Name: testServerName, Version: "1"})
	started := make(chan struct{})
	stopped := make(chan struct{})
	wantErr := errors.New("transport failed")

	err := server.RunContext(t.Context(),
		testTransport(func(ctx context.Context, _ *Server) error {
			close(started)
			<-ctx.Done()
			close(stopped)
			return ctx.Err()
		}),
		testTransport(func(ctx context.Context, _ *Server) error {
			select {
			case <-started:
			case <-ctx.Done():
				return ctx.Err()
			}
			return wantErr
		}),
	)
	if !errors.Is(err, wantErr) {
		t.Fatalf("got %v, want %v", err, wantErr)
	}
	select {
	case <-stopped:
	default:
		t.Fatal("Run returned before the remaining transport stopped")
	}
}
