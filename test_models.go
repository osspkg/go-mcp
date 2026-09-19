/*
 *  Copyright (c) 2026 Mikhail Knyazhev <markus621@yandex.com>. All rights reserved.
 *  Use of this source code is governed by a BSD 3-Clause license that can be found in the LICENSE file.
 */

package mcp

//go:generate go run github.com/mailru/easyjson/easyjson -output_filename=test_models_easyjson.go test_models.go

//easyjson:json
//nolint:recvcheck // easyjson generates json.Marshaler on a value and json.Unmarshaler on a pointer.
type testInput struct {
	Name string `json:"name" mcp:"description=person name"`
}

//easyjson:json
//nolint:recvcheck // easyjson generates json.Marshaler on a value and json.Unmarshaler on a pointer.
type testOutput struct {
	Greeting string `json:"greeting"`
}

//easyjson:json
//nolint:recvcheck // easyjson generates json.Marshaler on a value and json.Unmarshaler on a pointer.
type recursiveInput struct {
	Next *recursiveInput `json:"next,omitempty"`
}
