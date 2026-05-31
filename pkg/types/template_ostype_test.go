package types

import "testing"

func TestVMTemplateResolvedOSType(t *testing.T) {
	cases := []struct {
		name string
		in   OSType
		want OSType
	}{
		{"empty defaults to linux", "", OSTypeLinux},
		{"explicit linux", OSTypeLinux, OSTypeLinux},
		{"windows", OSTypeWindows, OSTypeWindows},
		{"unknown defaults to linux", OSType("bsd"), OSTypeLinux},
		{"mixed-case Windows resolves to windows", OSType("Windows"), OSTypeWindows},
		{"upper-case LINUX resolves to linux", OSType("LINUX"), OSTypeLinux},
		{"surrounding whitespace is trimmed", OSType("  windows  "), OSTypeWindows},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			tpl := VMTemplate{OSType: c.in}
			if got := tpl.ResolvedOSType(); got != c.want {
				t.Errorf("ResolvedOSType() = %q, want %q", got, c.want)
			}
		})
	}
}

func TestVMTemplateIsWindows(t *testing.T) {
	if (VMTemplate{OSType: OSTypeWindows}).IsWindows() != true {
		t.Error("windows template should report IsWindows() == true")
	}
	if (VMTemplate{}).IsWindows() != false {
		t.Error("empty template should report IsWindows() == false")
	}
	if (VMTemplate{OSType: OSTypeLinux}).IsWindows() != false {
		t.Error("linux template should report IsWindows() == false")
	}
}
