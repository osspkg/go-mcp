package stdio

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"go.osspkg.com/mcp"
)

func TestUnitServe(t *testing.T) {
	server, err := mcp.New(mcp.ServerInfo{Name: "test", Version: "1"})
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
	server, err := mcp.New(mcp.ServerInfo{Name: "test", Version: "1"})
	if err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	err = ServeWithConfig(t.Context(), server, strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"ping"}`+"\n"), &output, Config{MaxMessageBytes: 8})
	if err == nil {
		t.Fatal("expected framing limit error")
	}
}
