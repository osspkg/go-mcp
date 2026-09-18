package config

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"go.osspkg.com/mcp"
)

func TestUnitLoadYAML(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte("name: example\nversion: 1.2.3\nenable_stdio: false\nenable_http: true\nhttp_address: 127.0.0.1:9090\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	config, err := LoadYAML(path)
	if err != nil {
		t.Fatal(err)
	}
	if config.Name != "example" || !config.EnableHTTP || config.EnableStdio || config.HTTPAddress != "127.0.0.1:9090" {
		t.Fatalf("unexpected config: %#v", config)
	}
}

func TestUnitTransports(t *testing.T) {
	server, err := mcp.New(mcp.ServerInfo{Name: "test", Version: "1"})
	if err != nil {
		t.Fatal(err)
	}
	configuration := Default()
	configuration.EnableHTTP = true
	transports, err := Transports(configuration, server, bytes.NewBuffer(nil), &bytes.Buffer{})
	if err != nil {
		t.Fatal(err)
	}
	if len(transports) != 2 {
		t.Fatalf("got %d transports", len(transports))
	}
	if _, err := Transports(configuration, server, nil, nil); err == nil {
		t.Fatal("expected missing stdio streams error")
	}
}

func TestUnitLoadYAMLRejectsNestedData(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte("server:\n  name: example\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadYAML(path); err == nil {
		t.Fatal("expected error")
	}
}
