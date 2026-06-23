package types

import (
	"reflect"
	"testing"
)

func TestIsValidPCIAddress(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		{"0000:01:00.0", true},
		{"01:00.0", true},
		{"  01:00.0  ", true},
		{"0000:0a:1f.7", true},
		{"0000:01:20.0", false}, // slot must be 00-1f
		{"0000:01:ff.0", false},
		{"0000:01:00.8", false}, // function must be 0-7
		{"01:00", false},
		{"0000:01:00", false},
		{"gg:00.0", false},
		{"", false},
		{"0000:01:00.0 extra", false},
	}
	for _, c := range cases {
		if got := IsValidPCIAddress(c.in); got != c.want {
			t.Errorf("IsValidPCIAddress(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

func TestNormalizePCIAddress(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"01:00.0", "0000:01:00.0"},
		{"0000:01:00.0", "0000:01:00.0"},
		{"0000:01:00.1", "0000:01:00.1"},
		{"  0A:00.0 ", "0000:0a:00.0"},
		{"0000:01:20.0", ""},
		{"bogus", ""},
		{"", ""},
	}
	for _, c := range cases {
		if got := NormalizePCIAddress(c.in); got != c.want {
			t.Errorf("NormalizePCIAddress(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestPCIAddressParts(t *testing.T) {
	d, b, s, f, ok := PCIAddressParts("01:00.0")
	if !ok {
		t.Fatal("PCIAddressParts(01:00.0) returned ok=false")
	}
	if d != "0x0000" || b != "0x01" || s != "0x00" || f != "0x0" {
		t.Errorf("parts = %q %q %q %q, want 0x0000 0x01 0x00 0x0", d, b, s, f)
	}

	if _, _, _, _, ok := PCIAddressParts("nope"); ok {
		t.Error("PCIAddressParts(nope) returned ok=true")
	}
}

func TestResolvedGPUs(t *testing.T) {
	spec := VMSpec{GPUs: []string{"01:00.0", "0000:01:00.0", "01:00.1", "bogus", "  ", "0000:02:00.0"}}
	got := spec.ResolvedGPUs()
	want := []string{"0000:01:00.0", "0000:01:00.1", "0000:02:00.0"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("ResolvedGPUs() = %v, want %v", got, want)
	}

	if got := (VMSpec{}).ResolvedGPUs(); len(got) != 0 {
		t.Errorf("ResolvedGPUs() on empty spec = %v, want empty", got)
	}
}
