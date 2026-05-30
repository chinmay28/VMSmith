package types

import "testing"

func TestVMSpecResolvedOSType(t *testing.T) {
	cases := []struct {
		name string
		in   OSType
		want OSType
	}{
		{"empty defaults to linux", "", OSTypeLinux},
		{"explicit linux", OSTypeLinux, OSTypeLinux},
		{"windows", OSTypeWindows, OSTypeWindows},
		{"unknown defaults to linux", OSType("bsd"), OSTypeLinux},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			spec := VMSpec{OSType: c.in}
			if got := spec.ResolvedOSType(); got != c.want {
				t.Errorf("ResolvedOSType() = %q, want %q", got, c.want)
			}
		})
	}
}

func TestVMSpecIsWindows(t *testing.T) {
	if (VMSpec{OSType: OSTypeWindows}).IsWindows() != true {
		t.Error("windows spec should report IsWindows() == true")
	}
	if (VMSpec{}).IsWindows() != false {
		t.Error("empty spec should report IsWindows() == false")
	}
	if (VMSpec{OSType: OSTypeLinux}).IsWindows() != false {
		t.Error("linux spec should report IsWindows() == false")
	}
}

func TestIsKnownWindowsVariant(t *testing.T) {
	valid := []string{"windows-10", "windows-11", "windows-server-2019", "windows-server-2022", "windows-server-2025"}
	for _, v := range valid {
		if !IsKnownWindowsVariant(v) {
			t.Errorf("%q should be a known windows variant", v)
		}
	}
	for _, v := range []string{"", "windows-vista", "win10", "linux"} {
		if IsKnownWindowsVariant(v) {
			t.Errorf("%q should NOT be a known windows variant", v)
		}
	}
}
