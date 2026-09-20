/*
 *  Copyright (c) 2026 Mikhail Knyazhev <markus621@yandex.com>. All rights reserved.
 *  Use of this source code is governed by a BSD 3-Clause license that can be found in the LICENSE file.
 */

package client

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestNewStdioTransport_MaxMessageBytes(t *testing.T) {
	tests := []struct {
		name  string
		limit int
		want  int
		err   bool
	}{
		{name: "default", want: defaultMaxMessageBytes},
		{name: "small explicit limit", limit: 8, want: 8},
		{name: "largest safe limit", limit: maxStdioMessageBytes, want: maxStdioMessageBytes},
		{name: "unsafe limit", limit: maxStdioMessageBytes + 1, err: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			transport, err := NewStdioTransport(strings.NewReader(""), &strings.Builder{}, StdioConfig{MaxMessageBytes: test.limit})
			if test.err {
				if err == nil {
					t.Fatal("NewStdioTransport() error = nil, want error")
				}
				return
			}
			if err != nil {
				t.Fatalf("NewStdioTransport() error = %v", err)
			}
			if transport.config.MaxMessageBytes != test.want {
				t.Fatalf("MaxMessageBytes = %d, want %d", transport.config.MaxMessageBytes, test.want)
			}
		})
	}
}

func TestStdioTransport_StartHonorsSmallMaxMessageBytes(t *testing.T) {
	transport, err := NewStdioTransport(strings.NewReader("123456789\n"), &strings.Builder{}, StdioConfig{MaxMessageBytes: 8})
	if err != nil {
		t.Fatal(err)
	}
	err = transport.Start(context.Background(), func(context.Context, []byte) error {
		return nil
	})
	if err == nil {
		t.Fatal("Start() error = nil, want scanner error")
	}
	if errors.Is(err, ErrTransportClosed) {
		t.Fatalf("Start() error = %v, want scanner error", err)
	}
}

func TestStdioTransport_SendAppendsNewline(t *testing.T) {
	output := &strings.Builder{}
	transport, err := NewStdioTransport(strings.NewReader(""), output, StdioConfig{})
	if err != nil {
		t.Fatal(err)
	}
	transport.cancel = func() {}

	if _, err := transport.Send(context.Background(), []byte(`{"jsonrpc":"2.0"}`)); err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	if got, want := output.String(), "{\"jsonrpc\":\"2.0\"}\n"; got != want {
		t.Errorf("Send() output = %q, want %q", got, want)
	}
}

func TestNewCommandTransport_RejectsUnsafeMaxMessageBytes(t *testing.T) {
	_, err := NewCommandTransport("server", nil, CommandConfig{StdioConfig: StdioConfig{MaxMessageBytes: maxStdioMessageBytes + 1}})
	if err == nil {
		t.Fatal("NewCommandTransport() error = nil, want error")
	}
}
