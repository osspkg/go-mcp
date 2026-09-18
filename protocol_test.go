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

func TestUnitRegisterToolAndCall(t *testing.T) {
	server, err := New(ServerInfo{Name: "test", Version: "1"})
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

func TestUnitMiddlewareAuthorization(t *testing.T) {
	server, err := New(ServerInfo{Name: "test", Version: "1"}, WithMiddleware(func(next Handler) Handler {
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

func TestUnitResourcesAndPrompts(t *testing.T) {
	server, _ := New(ServerInfo{Name: "test", Version: "1"})
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
	server, _ := New(ServerInfo{Name: "test", Version: "1"})
	_, _ = server.ServeJSON(t.Context(), []byte(`{"jsonrpc":"2.0","id":1,"method":"ping"}`), RequestMeta{})
	err := server.RegisterResource(Resource{URI: "memo://one", Name: "one", Text: "content"})
	if !errors.Is(err, ErrStarted) {
		t.Fatalf("got %v, want ErrStarted", err)
	}
}

type testTransport func(context.Context, *Server) error

func (transport testTransport) Serve(ctx context.Context, server *Server) error {
	return transport(ctx, server)
}

func TestUnitRunCancelsTransports(t *testing.T) {
	server, _ := New(ServerInfo{Name: "test", Version: "1"})
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Millisecond)
	defer cancel()
	err := server.RunContext(ctx, testTransport(func(ctx context.Context, _ *Server) error { <-ctx.Done(); return ctx.Err() }))
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("got %v", err)
	}
}

func TestUnitRunWaitsForTransportShutdownAfterFailure(t *testing.T) {
	server, _ := New(ServerInfo{Name: "test", Version: "1"})
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
