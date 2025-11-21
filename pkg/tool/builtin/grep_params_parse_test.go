package toolbuiltin

import "testing"

func TestParseOutputMode(t *testing.T) {
	cases := []struct {
		name    string
		params  map[string]any
		want    string
		wantErr bool
	}{
		{"default", nil, "files_with_matches", false},
		{"content", map[string]any{"output_mode": "content"}, "content", false},
		{"files", map[string]any{"output_mode": "files_with_matches"}, "files_with_matches", false},
		{"count", map[string]any{"output_mode": "count"}, "count", false},
		{"empty", map[string]any{"output_mode": " "}, "", true},
		{"invalid_type", map[string]any{"output_mode": 3}, "", true},
		{"invalid_value", map[string]any{"output_mode": "bogus"}, "", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseOutputMode(tc.params)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error")
				}
				return
			}
			if err != nil || got != tc.want {
				t.Fatalf("got %q err=%v want %q", got, err, tc.want)
			}
		})
	}
}

func TestParseBoolParam(t *testing.T) {
	cases := []struct {
		name     string
		value    any
		want     bool
		provided bool
		wantErr  bool
	}{
		{"bool_true", true, true, true, false},
		{"bool_false", false, false, true, false},
		{"string_true", "true", true, true, false},
		{"string_false", "false", false, true, false},
		{"invalid_string", "nope", false, true, true},
		{"wrong_type", 1, false, true, true},
		{"missing", nil, false, false, false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			params := map[string]any{"flag": nil}
			if tc.value != nil {
				params["flag"] = tc.value
			}
			got, provided, err := parseBoolParam(params, "flag")
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.want || provided != tc.provided {
				t.Fatalf("got=%v provided=%v want=%v/%v", got, provided, tc.want, tc.provided)
			}
		})
	}
}

func TestParseGlobFilter(t *testing.T) {
	cases := []struct {
		name    string
		params  map[string]any
		want    string
		wantErr bool
	}{
		{"missing", nil, "", false},
		{"simple", map[string]any{"glob": "*.go"}, "*.go", false},
		{"trimmed", map[string]any{"glob": " *.js "}, "*.js", false},
		{"invalid_type", map[string]any{"glob": 3}, "", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseGlobFilter(tc.params)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error")
				}
				return
			}
			if err != nil || got != tc.want {
				t.Fatalf("got %q err=%v want %q", got, err, tc.want)
			}
		})
	}
}

func TestParseFileTypeFilter(t *testing.T) {
	cases := []struct {
		name    string
		params  map[string]any
		want    string
		wantErr bool
	}{
		{"missing", nil, "", false},
		{"simple", map[string]any{"type": "go"}, "go", false},
		{"trimmed", map[string]any{"type": " js "}, "js", false},
		{"invalid", map[string]any{"type": 3}, "", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseFileTypeFilter(tc.params)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error")
				}
				return
			}
			if err != nil || got != tc.want {
				t.Fatalf("got %q err=%v want %q", got, err, tc.want)
			}
		})
	}
}

func TestParseHeadLimit(t *testing.T) {
	cases := []struct {
		name    string
		params  map[string]any
		want    int
		wantErr bool
	}{
		{"missing", nil, 0, false},
		{"valid", map[string]any{"head_limit": 5}, 5, false},
		{"string", map[string]any{"head_limit": "7"}, 7, false},
		{"negative", map[string]any{"head_limit": -1}, 0, true},
		{"nonint", map[string]any{"head_limit": 1.2}, 0, true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseHeadLimit(tc.params)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error")
				}
				return
			}
			if err != nil || got != tc.want {
				t.Fatalf("got %d err=%v want %d", got, err, tc.want)
			}
		})
	}
}

func TestParseOffset(t *testing.T) {
	cases := []struct {
		name    string
		params  map[string]any
		want    int
		wantErr bool
	}{
		{"missing", nil, 0, false},
		{"valid", map[string]any{"offset": 2}, 2, false},
		{"string", map[string]any{"offset": "3"}, 3, false},
		{"negative", map[string]any{"offset": -1}, 0, true},
		{"invalid", map[string]any{"offset": true}, 0, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseOffset(tc.params)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error")
				}
				return
			}
			if err != nil || got != tc.want {
				t.Fatalf("got %d err=%v want %d", got, err, tc.want)
			}
		})
	}
}

func TestParseContextParams(t *testing.T) {
	cases := []struct {
		name    string
		params  map[string]any
		before  int
		after   int
		wantErr bool
	}{
		{"default", nil, 0, 0, false},
		{"after_only", map[string]any{"-A": 2}, 0, 2, false},
		{"before_only", map[string]any{"-B": 3}, 3, 0, false},
		{"combined", map[string]any{"-A": 1, "-B": 2}, 2, 1, false},
		{"clamped", map[string]any{"-C": 99}, 5, 5, false},
		{"negative", map[string]any{"-B": -1}, 0, 0, true},
		{"invalid_type", map[string]any{"-A": "bad"}, 0, 0, true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			before, after, err := parseContextParams(tc.params, 5)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if before != tc.before || after != tc.after {
				t.Fatalf("got before=%d after=%d want %d/%d", before, after, tc.before, tc.after)
			}
		})
	}
}
