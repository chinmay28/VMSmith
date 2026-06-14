package types

import "testing"

func boolPtr(v bool) *bool { return &v }

// TestResolvedSecureBoot covers the 5.6.9 resolution contract: explicit
// pointer wins; the windows-11 default only kicks in when the guest is
// already booting via EFI firmware so pre-5.6.9 SeaBIOS specs render
// unchanged.
func TestResolvedSecureBoot(t *testing.T) {
	cases := []struct {
		name string
		spec VMSpec
		want bool
	}{
		{"linux default off", VMSpec{}, false},
		{"windows default off", VMSpec{OSType: OSTypeWindows}, false},
		{"windows-11 on seabios stays off", VMSpec{OSType: OSTypeWindows, OSVariant: "windows-11"}, false},
		{"windows-11 on uefi defaults on", VMSpec{OSType: OSTypeWindows, OSVariant: "windows-11", Firmware: "uefi"}, true},
		{"windows-11 on ovmf defaults on", VMSpec{OSType: OSTypeWindows, OSVariant: "windows-11", Firmware: "ovmf"}, true},
		{"windows-11 case-insensitive variant", VMSpec{OSType: OSTypeWindows, OSVariant: " Windows-11 ", Firmware: "uefi"}, true},
		{"explicit true wins on linux+uefi", VMSpec{Firmware: "uefi", SecureBoot: boolPtr(true)}, true},
		{"explicit false opts windows-11 out", VMSpec{OSType: OSTypeWindows, OSVariant: "windows-11", Firmware: "uefi", SecureBoot: boolPtr(false)}, false},
		{"server-2022 has no default", VMSpec{OSType: OSTypeWindows, OSVariant: "windows-server-2022", Firmware: "uefi"}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.spec.ResolvedSecureBoot(); got != tc.want {
				t.Errorf("ResolvedSecureBoot() = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestResolvedTPM mirrors the Secure Boot table; unlike Secure Boot the
// TPM default is not EFI-gated (swtpm works fine under SeaBIOS).
func TestResolvedTPM(t *testing.T) {
	cases := []struct {
		name string
		spec VMSpec
		want bool
	}{
		{"linux default off", VMSpec{}, false},
		{"windows default off", VMSpec{OSType: OSTypeWindows}, false},
		{"windows-11 defaults on even on seabios", VMSpec{OSType: OSTypeWindows, OSVariant: "windows-11"}, true},
		{"explicit false opts windows-11 out", VMSpec{OSType: OSTypeWindows, OSVariant: "windows-11", TPM: boolPtr(false)}, false},
		{"explicit true on linux", VMSpec{TPM: boolPtr(true)}, true},
		{"server-2025 has no default", VMSpec{OSType: OSTypeWindows, OSVariant: "windows-server-2025"}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.spec.ResolvedTPM(); got != tc.want {
				t.Errorf("ResolvedTPM() = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestRedactConsoleSecrets ensures the persisted VNC password artifacts
// (5.1.8) never survive the API serialization helper.
func TestRedactConsoleSecrets(t *testing.T) {
	v := VM{
		ID:              "vm-1",
		VNCPasswordHash: "$2a$10$hash",
		VNCPasswordEnc:  "blob",
	}
	v.Spec.VNCPassword = "plaintext"

	got := v.RedactConsoleSecrets()
	if got.VNCPasswordHash != "" || got.VNCPasswordEnc != "" || got.Spec.VNCPassword != "" {
		t.Errorf("RedactConsoleSecrets left secrets: %+v", got)
	}
	// Original is untouched (value receiver).
	if v.VNCPasswordHash == "" {
		t.Error("RedactConsoleSecrets mutated the receiver")
	}
}
