package jsonstrict

import (
	"strings"
	"testing"
)

type decodeFixture struct {
	Name   string `json:"name"`
	Nested struct {
		Value int `json:"value"`
	} `json:"nested"`
}

func TestDecodeRejectsDuplicateKeysAtEveryObjectDepth(t *testing.T) {
	tests := []struct {
		name string
		data string
	}{
		{name: "root", data: `{"name":"first","name":"second"}`},
		{name: "nested object", data: `{"nested":{"value":1,"value":2}}`},
		{name: "object in array", data: `[{"value":1,"value":2}]`},
		{name: "escaped equivalent", data: `{"name":"first","\u006eame":"second"}`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var target any
			if err := Decode([]byte(tt.data), &target, false); err == nil || !strings.Contains(err.Error(), "duplicate") {
				t.Fatalf("Decode() error = %v, want duplicate-key error", err)
			}
		})
	}
}

func TestDecodeRequiresExactlyOneJSONValue(t *testing.T) {
	tests := []string{
		`{"name":"demo"} {"name":"second"}`,
		`{"name":"demo"} trailing`,
		``,
	}
	for _, data := range tests {
		var target decodeFixture
		if err := Decode([]byte(data), &target, false); err == nil {
			t.Fatalf("Decode(%q) error = nil, want exact-value error", data)
		}
	}
}

func TestDecodeAppliesOptionalUnknownFieldPolicy(t *testing.T) {
	data := []byte(`{"name":"demo","future":{"enabled":true}}`)
	var permissive decodeFixture
	if err := Decode(data, &permissive, false); err != nil {
		t.Fatalf("Decode(permissive) error = %v", err)
	}
	if permissive.Name != "demo" {
		t.Fatalf("Decode(permissive).Name = %q, want demo", permissive.Name)
	}

	var strict decodeFixture
	if err := Decode(data, &strict, true); err == nil || !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("Decode(strict) error = %v, want unknown-field error", err)
	}
}
