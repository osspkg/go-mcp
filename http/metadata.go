/*
 *  Copyright (c) 2026 Mikhail Knyazhev <markus621@yandex.com>. All rights reserved.
 *  Use of this source code is governed by a BSD-3-Clause license that can be found in the LICENSE file.
 */

package http

import (
	"net/http"

	"go.osspkg.com/mcp"
)

// MetadataHandler serves one OAuth/OIDC discovery document. It deliberately
// does not implement authentication or token issuance; those remain the
// application's responsibility.
type MetadataHandler struct {
	payload []byte
}

// NewProtectedResourceMetadataHandler creates a RFC 9728 metadata handler.
func NewProtectedResourceMetadataHandler(metadata mcp.ProtectedResourceMetadata) (*MetadataHandler, error) {
	payload, err := mcp.MarshalMetadata(metadata)
	if err != nil {
		return nil, err
	}
	return &MetadataHandler{payload: payload}, nil
}

// NewAuthorizationServerMetadataHandler creates an OAuth/OIDC discovery
// metadata handler.
func NewAuthorizationServerMetadataHandler(metadata mcp.AuthorizationServerMetadata) (*MetadataHandler, error) {
	payload, err := mcp.MarshalMetadata(metadata)
	if err != nil {
		return nil, err
	}
	return &MetadataHandler{payload: payload}, nil
}

// ServeHTTP serves GET discovery responses and rejects other methods.
func (handler *MetadataHandler) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodGet {
		writer.Header().Set("Allow", http.MethodGet)
		http.Error(writer, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	writer.Header().Set("Content-Type", "application/json")
	writer.Header().Set("Cache-Control", "public, max-age=300")
	writer.WriteHeader(http.StatusOK)
	_, _ = writer.Write(handler.payload)
}
