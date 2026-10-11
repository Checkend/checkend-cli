package sanitize

import (
	"encoding/json"
	"reflect"
	"regexp"
	"testing"
)

func TestCleanTextRemovesEscapeSequences(t *testing.T) {
	cases := map[string]string{
		"a\x1b[31mred\x1b[0m b":                          "ared b",
		"x\x1b]0;evil title\x07y":                        "xy",
		"x\x1b]8;;https://evil\x1b\\link\x1b]8;;\x1b\\y": "xlinky",
		"x\x1bPq#0;2;0;0;0\x1b\\y":                       "xy",
		"x\x1bcy":                                        "xy",
		"abc\x1b":                                        "abc",
	}
	for in, want := range cases {
		if got := Text(in); got != want {
			t.Errorf("Text(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestCleanTextRemovesControlsButKeepsNewlinesAndTabs(t *testing.T) {
	cases := map[string]string{
		"a\x00b\x07c\x08d\re\x7ff": "abcdef",
		"a\u009b31mb\u0085c":       "a31mbc",
		"line1\nline2\tx":          "line1\nline2\tx",
		"a‮b⁦c⁩d":                  "abcd",
		"a\x9bb":                   "a�b", // a raw C1 byte is not valid UTF-8
	}
	for in, want := range cases {
		if got := Text(in); got != want {
			t.Errorf("Text(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestOneLineFoldsNewlinesAndTabs(t *testing.T) {
	if got := OneLine("a\nb\tc"); got != "a b c" {
		t.Errorf("OneLine = %q", got)
	}
}

func TestDeepCleansEveryStringAndKey(t *testing.T) {
	var value any
	_ = json.Unmarshal([]byte(`{"k\u001b[1m":["v\u001b]0;t\u0007",1,null,{"x":"\u009bq"}],"n":2}`), &value)
	want := map[string]any{"k": []any{"v", float64(1), nil, map[string]any{"x": "q"}}, "n": float64(2)}
	if got := Deep(value); !reflect.DeepEqual(got, want) {
		t.Errorf("Deep = %#v", got)
	}
}

func TestJSONKeepsTheValueButEscapesControlAndBidiCharacters(t *testing.T) {
	value := map[string]any{"m": "a\x1b[31mb\u009bc‮d e\nok<&>"}
	text := string(JSON(value))
	var back any
	if err := json.Unmarshal([]byte(text), &back); err != nil || !reflect.DeepEqual(back, value) {
		t.Fatalf("round trip: %v %#v", err, back)
	}
	if regexp.MustCompile("[\x00-\x09\x0b-\x1f\u007f-\u009f  ‪-‮⁦-⁩]").MatchString(text) {
		t.Errorf("unsafe character in %q", text)
	}
	if !regexp.MustCompile(`<&>`).MatchString(text) {
		t.Errorf("HTML characters were escaped: %q", text)
	}
}

func TestIndentKeepsKeyOrderAndNumbers(t *testing.T) {
	raw := []byte(`{"z":1,"a":{"big":12345678901234567890,"s":"\u001b x` + "‮" + `"}}`)
	got := string(Indent(raw))
	want := "{\n  \"z\": 1,\n  \"a\": {\n    \"big\": 12345678901234567890,\n    \"s\": \"\\u001b x\\u202e\"\n  }\n}"
	if got != want {
		t.Errorf("Indent =\n%s\nwant\n%s", got, want)
	}
}
