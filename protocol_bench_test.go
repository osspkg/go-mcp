package mcp

import "testing"

func BenchmarkServeJSONPing(b *testing.B) {
	server, err := New(ServerInfo{Name: "bench", Version: "1"})
	if err != nil {
		b.Fatal(err)
	}
	payload := []byte(`{"jsonrpc":"2.0","id":1,"method":"ping"}`)
	b.ReportAllocs()
	for b.Loop() {
		if _, err := server.ServeJSON(b.Context(), payload, RequestMeta{}); err != nil {
			b.Fatal(err)
		}
	}
}
