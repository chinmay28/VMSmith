package vm

import (
	"strings"
	"testing"

	"github.com/vmsmith/vmsmith/pkg/types"
)

func specBoolPtr(v bool) *bool { return &v }

// TestGenerateDomainXML_SecureBootTPM covers the 5.6.9 rendering: the
// <firmware> feature block + SMM for Secure Boot and the swtpm-backed
// tpm-crb device, plus their absence on default specs.
func TestGenerateDomainXML_SecureBootTPM(t *testing.T) {
	cases := []struct {
		name           string
		spec           types.VMSpec
		xmlMustContain []string
		xmlMustNotHave []string
	}{
		{
			name: "windows-11 on uefi gets secure boot + tpm",
			spec: types.VMSpec{Name: "w11", CPUs: 4, RAMMB: 4096, OSType: types.OSTypeWindows, OSVariant: "windows-11", Firmware: "uefi"},
			xmlMustContain: []string{
				"firmware='efi'",
				"<feature enabled='yes' name='secure-boot'/>",
				"<feature enabled='yes' name='enrolled-keys'/>",
				"<smm state='on'/>",
				"<tpm model='tpm-crb'>",
				"<backend type='emulator' version='2.0'/>",
			},
		},
		{
			name: "windows-11 opted out renders neither",
			spec: types.VMSpec{Name: "w11-min", CPUs: 4, RAMMB: 4096, OSType: types.OSTypeWindows, OSVariant: "windows-11", Firmware: "uefi",
				SecureBoot: specBoolPtr(false), TPM: specBoolPtr(false)},
			xmlMustContain: []string{"firmware='efi'"},
			xmlMustNotHave: []string{"secure-boot", "<smm", "<tpm"},
		},
		{
			name:           "linux default has neither",
			spec:           types.VMSpec{Name: "lx", CPUs: 2, RAMMB: 2048},
			xmlMustNotHave: []string{"secure-boot", "<smm", "<tpm", "firmware="},
		},
		{
			name:           "explicit tpm on linux renders device only",
			spec:           types.VMSpec{Name: "lx-tpm", CPUs: 2, RAMMB: 2048, TPM: specBoolPtr(true)},
			xmlMustContain: []string{"<tpm model='tpm-crb'>"},
			xmlMustNotHave: []string{"secure-boot", "<smm"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			params := DomainParamsFromSpec(tc.spec, "/disk.qcow2", "", "vmsmith-net", "52:54:00:00:00:01")
			xml, err := GenerateDomainXML(params)
			if err != nil {
				t.Fatalf("GenerateDomainXML: %v", err)
			}
			for _, want := range tc.xmlMustContain {
				if !strings.Contains(xml, want) {
					t.Errorf("XML missing %q:\n%s", want, xml)
				}
			}
			for _, not := range tc.xmlMustNotHave {
				if strings.Contains(xml, not) {
					t.Errorf("XML unexpectedly contains %q:\n%s", not, xml)
				}
			}
		})
	}
}

// TestEnforceSecureBootTPM exercises the host-probe reconciliation: an
// explicit request fails hard when the host lacks swtpm / secboot OVMF,
// while a windows-11 default degrades to a warning + device drop.
func TestEnforceSecureBootTPM(t *testing.T) {
	stubProbes := func(t *testing.T, swtpm, ovmf bool) {
		t.Helper()
		origSwtpm, origOVMF := swtpmLookPath, secureBootFirmwareAvailable
		swtpmLookPath = func() bool { return swtpm }
		secureBootFirmwareAvailable = func() bool { return ovmf }
		t.Cleanup(func() {
			swtpmLookPath = origSwtpm
			secureBootFirmwareAvailable = origOVMF
		})
	}

	w11 := types.VMSpec{Name: "w11", OSType: types.OSTypeWindows, OSVariant: "windows-11", Firmware: "uefi"}

	t.Run("host has both: params untouched", func(t *testing.T) {
		stubProbes(t, true, true)
		params := DomainParamsFromSpec(w11, "/d", "", "net", "52:54:00:00:00:01")
		if err := enforceSecureBootTPM(&params, w11); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !params.SecureBoot || !params.TPM {
			t.Errorf("devices dropped on capable host: secureBoot=%v tpm=%v", params.SecureBoot, params.TPM)
		}
	})

	t.Run("defaulted devices degrade when host lacks both", func(t *testing.T) {
		stubProbes(t, false, false)
		params := DomainParamsFromSpec(w11, "/d", "", "net", "52:54:00:00:00:01")
		if err := enforceSecureBootTPM(&params, w11); err != nil {
			t.Fatalf("defaulted devices must degrade, got error: %v", err)
		}
		if params.SecureBoot || params.TPM {
			t.Errorf("devices not dropped: secureBoot=%v tpm=%v", params.SecureBoot, params.TPM)
		}
	})

	t.Run("explicit tpm fails hard without swtpm", func(t *testing.T) {
		stubProbes(t, false, true)
		spec := w11
		spec.TPM = specBoolPtr(true)
		params := DomainParamsFromSpec(spec, "/d", "", "net", "52:54:00:00:00:01")
		err := enforceSecureBootTPM(&params, spec)
		if err == nil {
			t.Fatal("expected tpm_unavailable error")
		}
		apiErr, ok := err.(*types.APIError)
		if !ok || apiErr.Code != "tpm_unavailable" {
			t.Errorf("error = %v, want tpm_unavailable APIError", err)
		}
	})

	t.Run("explicit secure boot fails hard without OVMF", func(t *testing.T) {
		stubProbes(t, true, false)
		spec := w11
		spec.SecureBoot = specBoolPtr(true)
		params := DomainParamsFromSpec(spec, "/d", "", "net", "52:54:00:00:00:01")
		err := enforceSecureBootTPM(&params, spec)
		if err == nil {
			t.Fatal("expected secure_boot_unavailable error")
		}
		apiErr, ok := err.(*types.APIError)
		if !ok || apiErr.Code != "secure_boot_unavailable" {
			t.Errorf("error = %v, want secure_boot_unavailable APIError", err)
		}
	})
}
