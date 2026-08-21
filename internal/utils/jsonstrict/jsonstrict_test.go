package jsonstrict

import (
	"encoding/json"
	"strings"
	"testing"
)

type decodeFixture struct {
	Name   string `json:"name"`
	Kelvin string `json:"K"`
	Nested struct {
		Value int `json:"value"`
	} `json:"nested"`
}

func TestDecodeRejectsEncodingJSONEquivalentKeysAtEveryObjectDepth(t *testing.T) {
	tests := []struct {
		name string
		data string
	}{
		{name: "exact root", data: `{"name":"first","name":"second"}`},
		{name: "ASCII case folded root", data: `{"name":"trusted","NAME":"shadow"}`},
		{name: "Unicode case folded root", data: `{"K":"trusted","\u212a":"shadow"}`},
		{name: "nested object", data: `{"nested":{"value":1,"value":2}}`},
		{name: "case folded object in array", data: `[{"value":1,"VALUE":2}]`},
		{name: "escaped equivalent", data: `{"name":"first","\u006eame":"second"}`},
	}

	for _, tt := range tests {
		for _, disallowUnknown := range []bool{false, true} {
			t.Run(tt.name+"/unknown="+map[bool]string{false: "allowed", true: "rejected"}[disallowUnknown], func(t *testing.T) {
				var target any = new(any)
				if disallowUnknown {
					target = &decodeFixture{}
				}
				if err := Decode([]byte(tt.data), target, disallowUnknown); err == nil || !strings.Contains(err.Error(), "duplicate") {
					t.Fatalf("Decode() error = %v, want semantic duplicate-key error", err)
				}
			})
		}
	}
}

func TestDecodeClosesEncodingJSONCaseFoldOverwrite(t *testing.T) {
	data := []byte(`{"name":"trusted","NAME":"shadow"}`)
	var ordinary decodeFixture
	if err := json.Unmarshal(data, &ordinary); err != nil {
		t.Fatal(err)
	}
	if ordinary.Name != "shadow" {
		t.Fatalf("encoding/json Name = %q, want proof that the folded key overwrites trusted data", ordinary.Name)
	}

	var strict decodeFixture
	if err := Decode(data, &strict, true); err == nil || !strings.Contains(err.Error(), "duplicate") {
		t.Fatalf("Decode() error = %v, want overwrite-preventing duplicate error", err)
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
