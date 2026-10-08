package jsonx

import (
	"encoding/json/jsontext"
	"testing"
)

func TestDecodeKeepsNumberText(t *testing.T) {
	v, err := Decode([]byte(`{"w":1080.0,"id":9007199254740993,"a":[1,"x",null,true]}`))
	if err != nil {
		t.Fatal(err)
	}
	m := v.(map[string]any)
	if m["w"] != Number("1080.0") || m["id"] != Number("9007199254740993") {
		t.Fatalf("%#v", m)
	}
	if _, err = Decode([]byte(`1 2`)); err == nil {
		t.Fatal("trailing data accepted")
	}
}

func TestMarshalMatchesV1(t *testing.T) {
	raw, err := Marshal(map[string]any{"b": "<&>\u2028", "a": []int(nil), "n": Number("2.0")})
	want := `{"a":null,"b":"\u003c\u0026\u003e\u2028","n":2.0}`
	if err != nil || string(raw) != want {
		t.Fatalf("%s %v", raw, err)
	}
	raw, _ = Marshal("<", jsontext.EscapeForHTML(false))
	if string(raw) != `"<"` {
		t.Fatalf("%s", raw)
	}
}
