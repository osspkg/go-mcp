/*
 *  Copyright (c) 2026 Mikhail Knyazhev <markus621@yandex.com>. All rights reserved.
 *  Use of this source code is governed by a BSD 3-Clause license that can be found in the LICENSE file.
 */

// Package main serves OAuth 2.0 and OpenID Connect discovery metadata.
package main

import (
	"log"
	"net/http"
	"time"

	"go.osspkg.com/mcp"
	mcphttp "go.osspkg.com/mcp/http"
)

const (
	discoveryReadTimeout  = 15 * time.Second
	discoveryWriteTimeout = 30 * time.Second
	discoveryIdleTimeout  = 60 * time.Second
)

func main() {
	protectedResource, err := mcphttp.NewProtectedResourceMetadataHandler(mcp.ProtectedResourceMetadata{
		Resource:               "https://mcp.example.test",
		AuthorizationServers:   []string{"https://auth.example.test"},
		ScopesSupported:        []string{"mcp"},
		BearerMethodsSupported: []string{"header"},
	})
	if err != nil {
		log.Fatal(err)
	}
	authorizationServer, err := mcphttp.NewAuthorizationServerMetadataHandler(mcp.AuthorizationServerMetadata{ //nolint:gosec // example URLs are public discovery metadata, not credentials
		Issuer:                 "https://auth.example.test",
		AuthorizationEndpoint:  "https://auth.example.test/authorize",
		TokenEndpoint:          "https://auth.example.test/token",
		GrantTypesSupported:    []string{"authorization_code"},
		ResponseTypesSupported: []string{"code"},
	})
	if err != nil {
		log.Fatal(err)
	}

	mux := http.NewServeMux()
	mux.Handle("/.well-known/oauth-protected-resource", protectedResource)
	mux.Handle("/.well-known/oauth-authorization-server", authorizationServer)
	log.Println("discovery metadata listening on :8090")
	server := &http.Server{Addr: ":8090", Handler: mux, ReadTimeout: discoveryReadTimeout, WriteTimeout: discoveryWriteTimeout, IdleTimeout: discoveryIdleTimeout}
	log.Fatal(server.ListenAndServe())
}
