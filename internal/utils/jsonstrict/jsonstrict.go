// Package jsonstrict 提供拒绝重复键和尾随值的 JSON 解码。
package jsonstrict

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

// Decode 解码唯一 JSON 值，并在正式解码前递归拒绝所有对象中的重复键。
// disallowUnknown 为 true 时，target 还必须是字段闭合的结构体模型。
func Decode(data []byte, target any, disallowUnknown bool) error {
	prewalk := json.NewDecoder(bytes.NewReader(data))
	if err := walkValue(prewalk); err != nil {
		return fmt.Errorf("validate JSON structure: %w", err)
	}
	if token, err := prewalk.Token(); !errors.Is(err, io.EOF) {
		if err == nil {
			return fmt.Errorf("validate trailing JSON token %v", token)
		}
		return fmt.Errorf("validate trailing JSON: %w", err)
	}

	decoder := json.NewDecoder(bytes.NewReader(data))
	if disallowUnknown {
		decoder.DisallowUnknownFields()
	}
	if err := decoder.Decode(target); err != nil {
		return fmt.Errorf("decode JSON value: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("decode trailing JSON value")
		}
		return fmt.Errorf("decode trailing JSON: %w", err)
	}
	return nil
}

func walkValue(decoder *json.Decoder) error {
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	delimiter, ok := token.(json.Delim)
	if !ok {
		return nil
	}

	switch delimiter {
	case '{':
		keys := make(map[string]struct{})
		for decoder.More() {
			keyToken, err := decoder.Token()
			if err != nil {
				return err
			}
			key, ok := keyToken.(string)
			if !ok {
				return fmt.Errorf("object key has type %T", keyToken)
			}
			if _, duplicate := keys[key]; duplicate {
				return fmt.Errorf("duplicate object key %q", key)
			}
			keys[key] = struct{}{}
			if err := walkValue(decoder); err != nil {
				return err
			}
		}
		return requireClosingDelimiter(decoder, '}')
	case '[':
		for decoder.More() {
			if err := walkValue(decoder); err != nil {
				return err
			}
		}
		return requireClosingDelimiter(decoder, ']')
	default:
		return fmt.Errorf("unexpected opening delimiter %q", delimiter)
	}
}

func requireClosingDelimiter(decoder *json.Decoder, want json.Delim) error {
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	if token != want {
		return fmt.Errorf("closing delimiter = %v, want %v", token, want)
	}
	return nil
}
