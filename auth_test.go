package mcp

import (
	"strings"
	"testing"
)

func TestUnitMarshalDiscoveryMetadata(t *testing.T) {
	payload, err := MarshalMetadata(ProtectedResourceMetadata{Resource: "https://api.example.test", ScopesSupported: []string{"mcp"}})
	if err != nil || !strings.Contains(string(payload), `"resource"`) {
		t.Fatalf("payload=%s err=%v", payload, err)
	}
	if _, err := MarshalMetadata(ProtectedResourceMetadata{}); err == nil {
		t.Fatal("accepted metadata without resource")
	}
	payload, err = MarshalMetadata(AuthorizationServerMetadata{Issuer: "https://auth.example.test", AuthorizationEndpoint: "https://auth.example.test/authorize"})
	if err != nil || !strings.Contains(string(payload), `"authorization_endpoint"`) {
		t.Fatalf("payload=%s err=%v", payload, err)
	}
}
