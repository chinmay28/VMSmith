package types

import "testing"

func TestVMSpecResolvedClockOffset(t *testing.T) {
	cases := []struct {
		name string
		spec VMSpec
		want string
	}{
		{"empty linux defaults to utc", VMSpec{}, ClockOffsetUTC},
		{"explicit linux defaults to utc", VMSpec{OSType: OSTypeLinux}, ClockOffsetUTC},
		{"empty windows defaults to localtime", VMSpec{OSType: OSTypeWindows}, ClockOffsetLocaltime},
		{"explicit utc on linux", VMSpec{OSType: OSTypeLinux, ClockOffset: "utc"}, ClockOffsetUTC},
		{"explicit utc on windows overrides default", VMSpec{OSType: OSTypeWindows, ClockOffset: "utc"}, ClockOffsetUTC},
		{"explicit localtime on linux overrides default", VMSpec{OSType: OSTypeLinux, ClockOffset: "localtime"}, ClockOffsetLocaltime},
		{"explicit localtime on windows", VMSpec{OSType: OSTypeWindows, ClockOffset: "localtime"}, ClockOffsetLocaltime},
		{"mixed-case UTC normalises", VMSpec{ClockOffset: "UTC"}, ClockOffsetUTC},
		{"mixed-case LocalTime normalises", VMSpec{OSType: OSTypeWindows, ClockOffset: "LocalTime"}, ClockOffsetLocaltime},
		{"surrounding whitespace trimmed", VMSpec{ClockOffset: "  utc  "}, ClockOffsetUTC},
		{"whitespace-only resolves to OS default", VMSpec{OSType: OSTypeWindows, ClockOffset: "   "}, ClockOffsetLocaltime},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := c.spec.ResolvedClockOffset(); got != c.want {
				t.Errorf("ResolvedClockOffset() = %q, want %q", got, c.want)
			}
		})
	}
}
