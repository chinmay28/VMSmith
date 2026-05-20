package api

import "testing"

func TestParseTristateBoolParam(t *testing.T) {
	cases := []struct {
		name      string
		raw       string
		wantVal   bool
		wantSet   bool
		wantError bool
		wantCode  string
	}{
		{name: "empty", raw: "", wantSet: false},
		{name: "whitespace_only", raw: "   ", wantSet: false},
		{name: "true_lower", raw: "true", wantVal: true, wantSet: true},
		{name: "true_upper", raw: "TRUE", wantVal: true, wantSet: true},
		{name: "true_mixed", raw: "TrUe", wantVal: true, wantSet: true},
		{name: "true_padded", raw: "  true  ", wantVal: true, wantSet: true},
		{name: "false_lower", raw: "false", wantVal: false, wantSet: true},
		{name: "false_upper", raw: "FALSE", wantVal: false, wantSet: true},
		{name: "numeric_one", raw: "1", wantVal: true, wantSet: true},
		{name: "numeric_zero", raw: "0", wantVal: false, wantSet: true},
		{name: "invalid_word", raw: "maybe", wantError: true, wantCode: "invalid_test_param"},
		{name: "invalid_number", raw: "2", wantError: true, wantCode: "invalid_test_param"},
		{name: "invalid_mixed", raw: "yes", wantError: true, wantCode: "invalid_test_param"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			val, set, err := parseTristateBoolParam(tc.raw, "test_param")
			if tc.wantError {
				if err == nil {
					t.Fatalf("expected error, got nil")
				}
				if err.Code != tc.wantCode {
					t.Fatalf("code = %q, want %q", err.Code, tc.wantCode)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if set != tc.wantSet {
				t.Fatalf("set = %v, want %v", set, tc.wantSet)
			}
			if val != tc.wantVal {
				t.Fatalf("val = %v, want %v", val, tc.wantVal)
			}
		})
	}
}
