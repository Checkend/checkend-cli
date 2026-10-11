// Package jv reads decoded JSON values (from encoding/json with UseNumber).
package jv

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
)

// Decode decodes JSON with numbers as json.Number.
func Decode(data []byte) (any, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	var v any
	err := dec.Decode(&v)
	return v, err
}

// Get follows keys and indexes (int) into a value. Missing parts give nil.
func Get(v any, path ...any) any {
	for _, p := range path {
		switch key := p.(type) {
		case string:
			m, ok := v.(map[string]any)
			if !ok {
				return nil
			}
			v = m[key]
		case int:
			a, ok := v.([]any)
			if !ok || key < 0 || key >= len(a) {
				return nil
			}
			v = a[key]
		}
	}
	return v
}

// Has reports whether an object has a key (also with a null value).
func Has(v any, key string) bool {
	m, ok := v.(map[string]any)
	if !ok {
		return false
	}
	_, ok = m[key]
	return ok
}

// List returns an array, or nil.
func List(v any) []any {
	a, _ := v.([]any)
	return a
}

// Str formats a value as JavaScript String() does; null gives "".
func Str(v any) string {
	switch x := v.(type) {
	case nil:
		return ""
	case string:
		return x
	case json.Number:
		return x.String()
	case bool, float64, int, int64:
		return fmt.Sprint(x)
	case []any:
		parts := make([]string, len(x))
		for i, item := range x {
			parts[i] = Str(item)
		}
		return strings.Join(parts, ",")
	default:
		return "[object Object]"
	}
}

// Or is Str, with dash for null and "".
func Or(v any, dash string) string {
	if s := Str(v); s != "" {
		return s
	}
	return dash
}

// Truthy is JavaScript truthiness.
func Truthy(v any) bool {
	switch x := v.(type) {
	case nil:
		return false
	case bool:
		return x
	case string:
		return x != ""
	case json.Number:
		f, err := x.Float64()
		return err != nil || f != 0
	}
	return true
}
