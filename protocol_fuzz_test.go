package mcp

import "testing"

func FuzzServeJSON(f *testing.F) {
	for _, payload := range [][]byte{nil, []byte(`{}`), []byte(`{"jsonrpc":"2.0","id":1,"method":"ping"}`)} {
		f.Add(payload)
	}
	f.Fuzz(func(t *testing.T, payload []byte) {
		server, err := New(ServerInfo{Name: "fuzz", Version: "1"})
		if err != nil {
			t.Fatal(err)
		}
		_, _ = server.ServeJSON(t.Context(), payload, RequestMeta{})
	})
}
