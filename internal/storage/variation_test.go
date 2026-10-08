package storage

import (
	"encoding/hex"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"os"
	"testing"
)

func typedValue(t *testing.T, raw jsontext.Value) any {
	t.Helper()
	var object map[string]jsontext.Value
	if json.Unmarshal(raw, &object, json.MatchCaseInsensitiveNames(true)) == nil && object != nil {
		if v, ok := object["sym"]; ok {
			var s string
			json.Unmarshal(v, &s, json.MatchCaseInsensitiveNames(true))
			return Symbol(s)
		}
		if v, ok := object["str"]; ok {
			var s string
			json.Unmarshal(v, &s, json.MatchCaseInsensitiveNames(true))
			return s
		}
		if v, ok := object["hash"]; ok {
			var entries [][]jsontext.Value
			json.Unmarshal(v, &entries, json.MatchCaseInsensitiveNames(true))
			result := Variation{}
			for _, e := range entries {
				var key string
				json.Unmarshal(e[0], &key, json.MatchCaseInsensitiveNames(true))
				result = append(result, Entry{key, typedValue(t, e[1])})
			}
			return result
		}
	}
	var array []jsontext.Value
	if len(raw) > 0 && raw[0] == '[' {
		json.Unmarshal(raw, &array, json.MatchCaseInsensitiveNames(true))
		result := []any{}
		for _, v := range array {
			result = append(result, typedValue(t, v))
		}
		return result
	}
	var n int64
	if string(raw) != "null" && json.Unmarshal(raw, &n, json.MatchCaseInsensitiveNames(true)) == nil {
		return n
	}
	var value any
	if err := json.Unmarshal(raw, &value, json.MatchCaseInsensitiveNames(true)); err != nil {
		t.Fatal(err)
	}
	return value
}
func TestVariationMarshalVectors(t *testing.T) {
	raw, err := os.ReadFile("../../reference/vectors/storage.json")
	if err != nil {
		t.Fatal(err)
	}
	var data struct {
		Variations []struct {
			Typed          jsontext.Value
			DecodedTyped   jsontext.Value `json:"decoded_typed"`
			Marshal        string         `json:"marshal_hex"`
			DecodedMarshal string         `json:"decoded_marshal_hex"`
			Digest         string
			DecodedDigest  string `json:"decoded_digest"`
		}
	}
	if err = json.Unmarshal(raw, &data, json.MatchCaseInsensitiveNames(true)); err != nil {
		t.Fatal(err)
	}
	for i, v := range data.Variations {
		for _, c := range []struct {
			raw             jsontext.Value
			marshal, digest string
		}{{v.Typed, v.Marshal, v.Digest}, {v.DecodedTyped, v.DecodedMarshal, v.DecodedDigest}} {
			variation := typedValue(t, c.raw).(Variation)
			if got := hex.EncodeToString(variation.MarshalRuby()); got != c.marshal {
				t.Errorf("case %d Marshal: %s want %s", i, got, c.marshal)
			}
			if got := variation.Digest(); got != c.digest {
				t.Errorf("case %d digest %s want %s", i, got, c.digest)
			}
		}
	}
}
