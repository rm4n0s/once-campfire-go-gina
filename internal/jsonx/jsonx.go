// Package jsonx holds what the packages on encoding/json/v2 share to keep the output the Rails
// reference expects: v1's escaping and ordering defaults, and decoding that keeps number text.
package jsonx

import (
	"bytes"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"io"
	"strconv"
)

// Number is a JSON number kept as written, so 1080.0 stays 1080.0 and large integers stay exact.
type Number string

// MarshalJSON writes the number bare, as encoding/json's Number does.
func (n Number) MarshalJSON() ([]byte, error) { return []byte(n), nil }
func (n Number) String() string               { return string(n) }
func (n Number) Float64() (float64, error)    { return strconv.ParseFloat(string(n), 64) }
func (n Number) Int64() (int64, error)        { return strconv.ParseInt(string(n), 10, 64) }

// Marshal encodes like encoding/json v1 did: <, > and & and U+2028/U+2029 escaped, object keys
// sorted, nil slices and maps as null. Later opts override these, e.g. jsontext.EscapeForHTML(false).
func Marshal(v any, opts ...json.Options) ([]byte, error) {
	return json.Marshal(v, json.JoinOptions(append([]json.Options{
		jsontext.EscapeForHTML(true),
		jsontext.EscapeForJS(true),
		json.Deterministic(true),
		json.FormatNilSliceAsNull(true),
		json.FormatNilMapAsNull(true),
	}, opts...)...))
}

// Line is Marshal plus the newline that v1's Encoder.Encode appended.
func Line(v any, opts ...json.Options) ([]byte, error) {
	raw, err := Marshal(v, opts...)
	if err != nil {
		return nil, err
	}
	return append(raw, '\n'), nil
}

// Decode parses exactly one JSON value. Objects become map[string]any, arrays []any, numbers Number.
func Decode(data []byte) (any, error) {
	d := jsontext.NewDecoder(bytes.NewReader(data), jsontext.AllowDuplicateNames(true))
	v, err := ReadValue(d)
	if err != nil {
		return nil, err
	}
	if _, err := d.ReadToken(); err != io.EOF {
		return nil, errors.New("jsonx: trailing data after top-level value")
	}
	return v, nil
}

// ReadValue reads the next value from d, as Decode does.
func ReadValue(d *jsontext.Decoder) (any, error) {
	tok, err := d.ReadToken()
	if err != nil {
		return nil, err
	}
	switch tok.Kind() {
	case '{':
		m := map[string]any{}
		for d.PeekKind() != '}' {
			key, err := d.ReadToken()
			if err != nil {
				return nil, err
			}
			name := key.String()
			if m[name], err = ReadValue(d); err != nil {
				return nil, err
			}
		}
		_, err := d.ReadToken()
		return m, err
	case '[':
		a := []any{}
		for d.PeekKind() != ']' {
			x, err := ReadValue(d)
			if err != nil {
				return nil, err
			}
			a = append(a, x)
		}
		_, err := d.ReadToken()
		return a, err
	case '"':
		return tok.String(), nil
	case '0':
		return Number(tok.String()), nil
	case 't', 'f':
		return tok.Bool(), nil
	default:
		return nil, nil
	}
}
