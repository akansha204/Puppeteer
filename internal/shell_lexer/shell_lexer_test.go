package shell_lexer

import (
	"reflect"
	"testing"
)

func TestFieldsQuoting(t *testing.T) {
	tests := []struct {
		in      string
		want    []string
		wantErr bool
	}{
		{in: "", want: nil},
		{in: "   ", want: nil},
		{in: "start worker sh -c \"echo hello world\"", want: []string{"start", "worker", "sh", "-c", "echo hello world"}},
		{in: "start x y 'a b' c", want: []string{"start", "x", "y", "a b", "c"}},
		{in: "a \"\" b", want: []string{"a", "", "b"}},
		{in: "a b\\ c", want: []string{"a", "b c"}},
		{in: "a \"x\\\"y\"", want: []string{"a", `x"y`}},
		{in: "a \"b\\\\c\"", want: []string{"a", `b\c`}},
		{in: "\"unclosed", wantErr: true},
		{in: "'unclosed", wantErr: true},
	}

	for _, tc := range tests {
		got, err := Fields(tc.in)
		if tc.wantErr {
			if err == nil {
				t.Errorf("Fields(%q) = %v, want error", tc.in, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("Fields(%q) error: %v", tc.in, err)
			continue
		}
		if !reflect.DeepEqual(got, tc.want) {
			t.Errorf("Fields(%q) = %#v, want %#v", tc.in, got, tc.want)
		}
	}
}
