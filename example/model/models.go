/*
 *  Copyright (c) 2026 Mikhail Knyazhev <markus621@yandex.com>. All rights reserved.
 *  Use of this source code is governed by a BSD 3-Clause license that can be found in the LICENSE file.
 */

// Package model contains generated JSON models used by runnable examples.
package model

//go:generate go run github.com/mailru/easyjson/easyjson -output_filename=models_easyjson.go models.go

// GreetingInput is the input for the stdio greeting example.
//
//easyjson:json
//nolint:recvcheck // easyjson generates json.Marshaler on a value and json.Unmarshaler on a pointer.
type GreetingInput struct {
	Name string `json:"name" mcp:"description=Name to greet"`
}

// GreetingOutput is the response from the stdio greeting example.
//
//easyjson:json
//nolint:recvcheck // easyjson generates json.Marshaler on a value and json.Unmarshaler on a pointer.
type GreetingOutput struct {
	Greeting string `json:"greeting"`
}

// EchoInput is the input for the SSE echo example.
//
//easyjson:json
//nolint:recvcheck // easyjson generates json.Marshaler on a value and json.Unmarshaler on a pointer.
type EchoInput struct {
	Message string `json:"message" mcp:"description=Message to echo"`
}

// EchoOutput is the response from the SSE echo example.
//
//easyjson:json
//nolint:recvcheck // easyjson generates json.Marshaler on a value and json.Unmarshaler on a pointer.
type EchoOutput struct {
	Message string `json:"message"`
}

// TimeInput is the input for the streamable HTTP time example.
//
//easyjson:json
//nolint:recvcheck // easyjson generates json.Marshaler on a value and json.Unmarshaler on a pointer.
type TimeInput struct {
	Timezone string `json:"timezone,omitempty" mcp:"description=Timezone to include in the response"`
}

// TimeOutput is the response from the streamable HTTP time example.
//
//easyjson:json
//nolint:recvcheck // easyjson generates json.Marshaler on a value and json.Unmarshaler on a pointer.
type TimeOutput struct {
	Message string `json:"message"`
}

// SecretInput is the input for the authorization middleware example.
//
//easyjson:json
//nolint:recvcheck // easyjson generates json.Marshaler on a value and json.Unmarshaler on a pointer.
type SecretInput struct {
	Question string `json:"question" mcp:"description=Question for the protected tool"`
}

// SecretOutput is the response from the authorization middleware example.
//
//easyjson:json
//nolint:recvcheck // easyjson generates json.Marshaler on a value and json.Unmarshaler on a pointer.
type SecretOutput struct {
	Answer string `json:"answer"`
}

// ConfigInput is the input for the configuration examples.
//
//easyjson:json
//nolint:recvcheck // easyjson generates json.Marshaler on a value and json.Unmarshaler on a pointer.
type ConfigInput struct {
	Value string `json:"value" mcp:"description=Value to echo"`
}

// ConfigOutput is the response from the configuration examples.
//
//easyjson:json
//nolint:recvcheck // easyjson generates json.Marshaler on a value and json.Unmarshaler on a pointer.
type ConfigOutput struct {
	Value string `json:"value"`
}

// FeatureInput is the input for the full-featured example.
//
//easyjson:json
//nolint:recvcheck // easyjson generates json.Marshaler on a value and json.Unmarshaler on a pointer.
type FeatureInput struct {
	Name string `json:"name" mcp:"description=Name to greet"`
}

// FeatureOutput is the response from the full-featured example.
//
//easyjson:json
//nolint:recvcheck // easyjson generates json.Marshaler on a value and json.Unmarshaler on a pointer.
type FeatureOutput struct {
	Greeting string `json:"greeting"`
}

// JobInput is the input for the background-job example.
//
//easyjson:json
//nolint:recvcheck // easyjson generates json.Marshaler on a value and json.Unmarshaler on a pointer.
type JobInput struct {
	DelayMS int `json:"delayMs,omitempty" mcp:"description=Artificial delay in milliseconds"`
}

// JobOutput is the response from the background-job example.
//
//easyjson:json
//nolint:recvcheck // easyjson generates json.Marshaler on a value and json.Unmarshaler on a pointer.
type JobOutput struct {
	Status string `json:"status"`
}
