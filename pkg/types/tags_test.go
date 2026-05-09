package types

import "testing"

func equalSlice(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestBulkTagAction_IsValid(t *testing.T) {
	cases := []struct {
		in   BulkTagAction
		want bool
	}{
		{BulkTagActionAdd, true},
		{BulkTagActionRemove, true},
		{BulkTagActionSet, true},
		{"", false},
		{"replace", false},
		{"ADD", false}, // exported constants are lowercase; callers normalize first
	}
	for _, c := range cases {
		if got := c.in.IsValid(); got != c.want {
			t.Errorf("BulkTagAction(%q).IsValid() = %v, want %v", c.in, got, c.want)
		}
	}
}

func TestMergeTags_Add(t *testing.T) {
	cases := []struct {
		name      string
		current   []string
		requested []string
		want      []string
	}{
		{name: "into empty", current: nil, requested: []string{"prod", "web"}, want: []string{"prod", "web"}},
		{name: "appends new", current: []string{"prod"}, requested: []string{"web"}, want: []string{"prod", "web"}},
		{name: "case-insensitive dedupe", current: []string{"prod"}, requested: []string{"PROD"}, want: []string{"prod"}},
		{name: "sorts result", current: []string{"prod", "web"}, requested: []string{"db", "cache"}, want: []string{"cache", "db", "prod", "web"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := MergeTags(BulkTagActionAdd, c.current, c.requested)
			if !equalSlice(got, c.want) {
				t.Errorf("MergeTags add: got %v, want %v", got, c.want)
			}
		})
	}
}

func TestMergeTags_Remove(t *testing.T) {
	cases := []struct {
		name      string
		current   []string
		requested []string
		want      []string
	}{
		{name: "removes match", current: []string{"prod", "web"}, requested: []string{"web"}, want: []string{"prod"}},
		{name: "case-insensitive", current: []string{"prod", "Web"}, requested: []string{"WEB"}, want: []string{"prod"}},
		{name: "no-op on missing", current: []string{"prod"}, requested: []string{"absent"}, want: []string{"prod"}},
		{name: "removes all", current: []string{"a", "b"}, requested: []string{"a", "b"}, want: []string{}},
		{name: "preserves current order", current: []string{"z", "a", "m"}, requested: []string{"a"}, want: []string{"z", "m"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := MergeTags(BulkTagActionRemove, c.current, c.requested)
			if !equalSlice(got, c.want) {
				t.Errorf("MergeTags remove: got %v, want %v", got, c.want)
			}
		})
	}
}

func TestMergeTags_Set(t *testing.T) {
	cases := []struct {
		name      string
		current   []string
		requested []string
		want      []string
	}{
		{name: "replaces", current: []string{"old"}, requested: []string{"new"}, want: []string{"new"}},
		{name: "clears", current: []string{"old", "stale"}, requested: []string{}, want: []string{}},
		{name: "copies (caller-safe)", current: []string{"old"}, requested: []string{"a", "b"}, want: []string{"a", "b"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := MergeTags(BulkTagActionSet, c.current, c.requested)
			if !equalSlice(got, c.want) {
				t.Errorf("MergeTags set: got %v, want %v", got, c.want)
			}
		})
	}
}

func TestMergeTags_UnknownActionFallsBack(t *testing.T) {
	got := MergeTags(BulkTagAction("bogus"), []string{"prod"}, []string{"web"})
	want := []string{"prod"}
	if !equalSlice(got, want) {
		t.Errorf("unknown action: got %v, want %v", got, want)
	}
}

func TestMergeTags_DoesNotMutateInputs(t *testing.T) {
	current := []string{"prod", "web"}
	requested := []string{"db"}
	out := MergeTags(BulkTagActionAdd, current, requested)
	if len(current) != 2 || current[0] != "prod" || current[1] != "web" {
		t.Errorf("current mutated: %v", current)
	}
	if len(requested) != 1 || requested[0] != "db" {
		t.Errorf("requested mutated: %v", requested)
	}
	// Mutating output must not alias inputs.
	out[0] = "mutated"
	if current[0] == "mutated" {
		t.Errorf("output aliases current slice")
	}
}

func TestTagsEqualSet(t *testing.T) {
	cases := []struct {
		name string
		a    []string
		b    []string
		want bool
	}{
		{name: "both empty", a: nil, b: nil, want: true},
		{name: "empty vs nil", a: []string{}, b: nil, want: true},
		{name: "same order", a: []string{"a", "b"}, b: []string{"a", "b"}, want: true},
		{name: "reordered", a: []string{"b", "a"}, b: []string{"a", "b"}, want: true},
		{name: "case-insensitive", a: []string{"PROD"}, b: []string{"prod"}, want: true},
		{name: "different sizes", a: []string{"a"}, b: []string{"a", "b"}, want: false},
		{name: "different content", a: []string{"a", "b"}, b: []string{"a", "c"}, want: false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := TagsEqualSet(c.a, c.b); got != c.want {
				t.Errorf("TagsEqualSet(%v, %v) = %v, want %v", c.a, c.b, got, c.want)
			}
		})
	}
}
