package stdio

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"strings"
	"testing"
	"time"

	"go.osspkg.com/mcp"
)

const testServerName = "test"

func TestUnitServe(t *testing.T) {
	server, err := mcp.New(mcp.ServerInfo{Name: testServerName, Version: "1"})
	if err != nil {
		t.Fatal(err)
	}
	input := strings.NewReader("{\"jsonrpc\":\"2.0\",\"id\":1,\"method\":\"ping\"}\n")
	var output bytes.Buffer
	if err := Serve(context.Background(), server, input, &output); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), `"result":{}`) {
		t.Fatalf("unexpected output: %s", output.String())
	}
}

func TestUnitServeWithConfigLimit(t *testing.T) {
	server, err := mcp.New(mcp.ServerInfo{Name: testServerName, Version: "1"})
	if err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	err = ServeWithConfig(t.Context(), server, strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"ping"}`+"\n"), &output, Config{MaxMessageBytes: 8})
	if err == nil {
		t.Fatal("expected framing limit error")
	}
}

type channelWriter struct {
	lines chan []byte
}

func (writer *channelWriter) Write(payload []byte) (int, error) {
	writer.lines <- append([]byte(nil), payload...)
	return len(payload), nil
}

func TestUnitServeRoutesClientResponses(t *testing.T) {
	server, err := mcp.New(mcp.ServerInfo{Name: testServerName, Version: "1"}, mcp.WithMiddleware(func(next mcp.Handler) mcp.Handler {
		return func(ctx context.Context, request mcp.Request) (any, error) {
			if request.Method != "ping" {
				return next(ctx, request)
			}
			return request.ListRoots(ctx)
		}
	}))
	if err != nil {
		t.Fatal(err)
	}
	inputReader, inputWriter := io.Pipe()
	output := &channelWriter{lines: make(chan []byte, 4)}
	serveDone := make(chan error, 1)
	go func() { serveDone <- Serve(t.Context(), server, inputReader, output) }()
	if _, err := inputWriter.Write([]byte(`{"jsonrpc":"2.0","id":1,"method":"ping"}` + "\n")); err != nil {
		t.Fatal(err)
	}
	var request struct {
		ID string `json:"id"`
	}
	select {
	case line := <-output.lines:
		if err := json.Unmarshal(line, &request); err != nil || request.ID == "" {
			t.Fatalf("request=%s err=%v", line, err)
		}
	case <-time.After(time.Second):
		t.Fatal("client request was not written")
	}
	response := `{"jsonrpc":"2.0","id":"` + request.ID + `","result":{"roots":[]}}` + "\n"
	if _, err := inputWriter.Write([]byte(response)); err != nil {
		t.Fatal(err)
	}
	select {
	case line := <-output.lines:
		if !bytes.Contains(line, []byte(`"result":{"roots":[]}`)) {
			t.Fatalf("unexpected response: %s", line)
		}
	case <-time.After(time.Second):
		t.Fatal("server response was not written")
	}
	if err := inputWriter.Close(); err != nil {
		t.Fatal(err)
	}
	if err := <-serveDone; err != nil {
		t.Fatal(err)
	}
}
