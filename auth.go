/*
 *  Copyright (c) 2026 Mikhail Knyazhev <markus621@yandex.com>. All rights reserved.
 *  Use of this source code is governed by a BSD 3-Clause license that can be found in the LICENSE file.
 */

package mcp

import (
	"encoding/json"
	"errors"
)

// ProtectedResourceMetadata describes an OAuth 2.0 protected MCP resource
// according to RFC 9728.
type ProtectedResourceMetadata struct {
	Resource                      string   `json:"resource"`
	AuthorizationServers          []string `json:"authorization_servers,omitempty"`
	ScopesSupported               []string `json:"scopes_supported,omitempty"`
	BearerMethodsSupported        []string `json:"bearer_methods_supported,omitempty"`
	ResourceName                  string   `json:"resource_name,omitempty"`
	ResourceDocumentation         string   `json:"resource_documentation,omitempty"`
	ResourcePolicyURI             string   `json:"resource_policy_uri,omitempty"`
	ResourceTOSURI                string   `json:"resource_tos_uri,omitempty"`
	DPoPSigningAlgValuesSupported []string `json:"dpop_signing_alg_values_supported,omitempty"`
}

// AuthorizationServerMetadata describes OAuth 2.0 and OpenID Connect
// authorization-server discovery metadata.
type AuthorizationServerMetadata struct {
	Issuer                            string   `json:"issuer"`
	AuthorizationEndpoint             string   `json:"authorization_endpoint"`
	TokenEndpoint                     string   `json:"token_endpoint,omitempty"`
	JWKSURI                           string   `json:"jwks_uri,omitempty"`
	RegistrationEndpoint              string   `json:"registration_endpoint,omitempty"`
	ScopesSupported                   []string `json:"scopes_supported,omitempty"`
	ResponseTypesSupported            []string `json:"response_types_supported,omitempty"`
	ResponseModesSupported            []string `json:"response_modes_supported,omitempty"`
	GrantTypesSupported               []string `json:"grant_types_supported,omitempty"`
	TokenEndpointAuthMethodsSupported []string `json:"token_endpoint_auth_methods_supported,omitempty"`
	CodeChallengeMethodsSupported     []string `json:"code_challenge_methods_supported,omitempty"`
	ServiceDocumentation              string   `json:"service_documentation,omitempty"`
	ClientIDMetadataDocumentSupported bool     `json:"client_id_metadata_document_supported,omitempty"`
	UserInfoEndpoint                  string   `json:"userinfo_endpoint,omitempty"`
	EndSessionEndpoint                string   `json:"end_session_endpoint,omitempty"`
	IDTokenSigningAlgValuesSupported  []string `json:"id_token_signing_alg_values_supported,omitempty"`
}

// MarshalMetadata validates and encodes discovery metadata as JSON. It is
// useful for applications that expose the well-known endpoints themselves.
func MarshalMetadata(metadata any) ([]byte, error) {
	switch value := metadata.(type) {
	case ProtectedResourceMetadata:
		if value.Resource == "" {
			return nil, errors.New("mcp: protected resource metadata requires resource")
		}
	case *ProtectedResourceMetadata:
		if value == nil || value.Resource == "" {
			return nil, errors.New("mcp: protected resource metadata requires resource")
		}
	case AuthorizationServerMetadata:
		if value.Issuer == "" || value.AuthorizationEndpoint == "" {
			return nil, errors.New("mcp: authorization metadata requires issuer and authorization endpoint")
		}
	case *AuthorizationServerMetadata:
		if value == nil || value.Issuer == "" || value.AuthorizationEndpoint == "" {
			return nil, errors.New("mcp: authorization metadata requires issuer and authorization endpoint")
		}
	default:
		return nil, errors.New("mcp: unsupported discovery metadata type")
	}
	return json.Marshal(metadata)
}
