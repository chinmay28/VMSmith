package vm

import (
	"strings"
	"testing"

	"github.com/vmsmith/vmsmith/pkg/types"
)

func boolPtr(v bool) *bool { return &v }

func renderSpec(t *testing.T, spec types.VMSpec) string {
	t.Helper()
	params := DomainParamsFromSpec(spec, "/tmp/disk.qcow2", "/tmp/cidata.iso", "vmsmith-net", "52:54:00:00:00:01")
	xml, err := GenerateDomainXML(params)
	if err != nil {
		t.Fatalf("GenerateDomainXML: %v", err)
	}
	return xml
}

func TestDomainXML_SecureBootEmitsFirmwareFeatures(t *testing.T) {
	xml := renderSpec(t, types.VMSpec{
		Name: "sb", CPUs: 2, RAMMB: 4096, OSType: types.OSTypeWindows,
		Firmware: "uefi", SecureBoot: boolPtr(true),
	})
	if !strings.Contains(xml, "firmware='efi'") {
		t.Errorf("missing firmware='efi':\n%s", xml)
	}
	if !strings.Contains(xml, `<feature enabled='yes' name='secure-boot'/>`) ||
		!strings.Contains(xml, `<feature enabled='yes' name='enrolled-keys'/>`) {
		t.Errorf("missing secure-boot firmware features:\n%s", xml)
	}
}

func TestDomainXML_SecureBootForcesEFIWithoutExplicitFirmware(t *testing.T) {
	xml := renderSpec(t, types.VMSpec{
		Name: "sb-auto", CPUs: 2, RAMMB: 4096,
		SecureBoot: boolPtr(true),
	})
	if !strings.Contains(xml, "firmware='efi'") {
		t.Errorf("secure boot should force firmware='efi':\n%s", xml)
	}
}

func TestDomainXML_TPMDevice(t *testing.T) {
	xml := renderSpec(t, types.VMSpec{
		Name: "tpm", CPUs: 2, RAMMB: 4096, TPM: boolPtr(true),
	})
	if !strings.Contains(xml, "<tpm model='tpm-crb'>") ||
		!strings.Contains(xml, "<backend type='emulator' version='2.0'/>") {
		t.Errorf("missing emulated TPM device:\n%s", xml)
	}
}

func TestDomainXML_Windows11DefaultsOnSecureBootAndTPM(t *testing.T) {
	xml := renderSpec(t, types.VMSpec{
		Name: "win11", CPUs: 4, RAMMB: 8192,
		OSType: types.OSTypeWindows, OSVariant: "windows-11",
	})
	if !strings.Contains(xml, "firmware='efi'") {
		t.Errorf("windows-11 should default to EFI firmware:\n%s", xml)
	}
	if !strings.Contains(xml, `<feature enabled='yes' name='secure-boot'/>`) {
		t.Errorf("windows-11 should default Secure Boot on:\n%s", xml)
	}
	if !strings.Contains(xml, "<tpm model='tpm-crb'>") {
		t.Errorf("windows-11 should default the TPM on:\n%s", xml)
	}
}

func TestDomainXML_Windows11ExplicitOptOut(t *testing.T) {
	xml := renderSpec(t, types.VMSpec{
		Name: "win11-optout", CPUs: 4, RAMMB: 8192,
		OSType: types.OSTypeWindows, OSVariant: "windows-11",
		SecureBoot: boolPtr(false), TPM: boolPtr(false),
	})
	if strings.Contains(xml, "name='secure-boot'") {
		t.Errorf("explicit secure_boot=false must win over the windows-11 default:\n%s", xml)
	}
	if strings.Contains(xml, "<tpm ") {
		t.Errorf("explicit tpm=false must win over the windows-11 default:\n%s", xml)
	}
}

func TestDomainXML_NonWindowsDefaultsOff(t *testing.T) {
	xml := renderSpec(t, types.VMSpec{Name: "linux", CPUs: 2, RAMMB: 2048})
	if strings.Contains(xml, "name='secure-boot'") || strings.Contains(xml, "<tpm ") {
		t.Errorf("linux default must not enable secure boot / TPM:\n%s", xml)
	}
	if strings.Contains(xml, "firmware=") {
		t.Errorf("linux default must stay on SeaBIOS (no firmware attr):\n%s", xml)
	}
}

func TestDomainXML_InstallISOAttachesBootCDROM(t *testing.T) {
	xml := renderSpec(t, types.VMSpec{
		Name: "win-install", CPUs: 4, RAMMB: 8192,
		OSType: types.OSTypeWindows, InstallISO: "/isos/win2022.iso",
	})
	if !strings.Contains(xml, "<source file='/isos/win2022.iso'/>") {
		t.Errorf("missing install ISO cdrom:\n%s", xml)
	}
	if !strings.Contains(xml, "<target dev='sdd' bus='sata'/>") {
		t.Errorf("install ISO should sit on sdd:\n%s", xml)
	}
	// cdrom boot entry must precede the hd entry so the installer runs first.
	cdromIdx := strings.Index(xml, "<boot dev='cdrom'/>")
	hdIdx := strings.Index(xml, "<boot dev='hd'/>")
	if cdromIdx == -1 || hdIdx == -1 || cdromIdx > hdIdx {
		t.Errorf("boot order should be cdrom then hd:\n%s", xml)
	}
}

func TestDomainXML_NoInstallISOMeansNoCDROMBoot(t *testing.T) {
	xml := renderSpec(t, types.VMSpec{Name: "plain", CPUs: 2, RAMMB: 2048})
	if strings.Contains(xml, "<boot dev='cdrom'/>") {
		t.Errorf("plain VM should not boot from cdrom:\n%s", xml)
	}
}

func TestGenerateAutounattendXML_UEFILayout(t *testing.T) {
	xml, err := GenerateAutounattendXML("win-guest", "S3cret!", "", 0, true)
	if err != nil {
		t.Fatalf("GenerateAutounattendXML: %v", err)
	}
	for _, want := range []string{
		"<Type>EFI</Type>",
		"<Type>MSR</Type>",
		"<PartitionID>3</PartitionID>",
		"<UILanguage>en-US</UILanguage>",
		"<ComputerName>win-guest</ComputerName>",
		"<Value>S3cret!</Value>",
		"<AcceptEula>true</AcceptEula>",
		"<fDenyTSConnections>false</fDenyTSConnections>",
		"winrm quickconfig",
	} {
		if !strings.Contains(xml, want) {
			t.Errorf("missing %q in autounattend:\n%s", want, xml)
		}
	}
	// The Active partition flag is BIOS-only; scope the check to the disk
	// configuration (the firewall group in specialize also uses <Active>).
	diskCfg := xml[strings.Index(xml, "<DiskConfiguration>"):strings.Index(xml, "</DiskConfiguration>")]
	if strings.Contains(diskCfg, "<Active>true</Active>") {
		t.Errorf("UEFI layout must not mark a partition active:\n%s", diskCfg)
	}
}

func TestGenerateAutounattendXML_BIOSLayout(t *testing.T) {
	xml, err := GenerateAutounattendXML("win-guest", "pw", "de-DE", 2, false)
	if err != nil {
		t.Fatalf("GenerateAutounattendXML: %v", err)
	}
	if strings.Contains(xml, "<Type>EFI</Type>") {
		t.Errorf("BIOS layout must not create an EFI partition:\n%s", xml)
	}
	for _, want := range []string{
		"<Active>true</Active>",
		"<PartitionID>1</PartitionID>",
		"<UILanguage>de-DE</UILanguage>",
		"<Key>/IMAGE/INDEX</Key>",
		"<Value>2</Value>",
	} {
		if !strings.Contains(xml, want) {
			t.Errorf("missing %q in autounattend:\n%s", want, xml)
		}
	}
}

func TestGenerateAutounattendXML_EscapesPassword(t *testing.T) {
	xml, err := GenerateAutounattendXML("g", `p<&>'"w`, "", 0, true)
	if err != nil {
		t.Fatalf("GenerateAutounattendXML: %v", err)
	}
	if strings.Contains(xml, `p<&>`) {
		t.Errorf("password not escaped:\n%s", xml)
	}
	if !strings.Contains(xml, "p&lt;&amp;&gt;&apos;&quot;w") {
		t.Errorf("expected escaped password in:\n%s", xml)
	}
}

func TestWindowsComputerName_Sanitizes(t *testing.T) {
	cases := map[string]string{
		"my-vm":                            "my-vm",
		"my_vm.prod":                       "my-vm-prod",
		"a-very-long-virtual-machine-name": "a-very-long-vir",
		"12345":                            "vmsmith-guest",
		"---":                              "vmsmith-guest",
	}
	for in, want := range cases {
		if got := windowsComputerName(in); got != want {
			t.Errorf("windowsComputerName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestProbeUEFIRequirements_Stubbed(t *testing.T) {
	origSwtpm, origOVMF, origISO := probeSwtpm, probeOVMF, probeInstallISO
	defer func() { probeSwtpm, probeOVMF, probeInstallISO = origSwtpm, origOVMF, origISO }()

	var swtpmCalled, ovmfCalled, isoCalled bool
	var ovmfSecure bool
	probeSwtpm = func() error { swtpmCalled = true; return nil }
	probeOVMF = func(secureBoot bool) error { ovmfCalled = true; ovmfSecure = secureBoot; return nil }
	probeInstallISO = func(path string) error { isoCalled = true; return nil }

	spec := types.VMSpec{
		Name: "win11", OSType: types.OSTypeWindows, OSVariant: "windows-11",
		InstallISO: "/isos/win11.iso",
	}
	if err := probeUEFIRequirements(spec); err != nil {
		t.Fatalf("probeUEFIRequirements: %v", err)
	}
	if !swtpmCalled || !ovmfCalled || !isoCalled {
		t.Errorf("probes called = swtpm:%t ovmf:%t iso:%t, want all true", swtpmCalled, ovmfCalled, isoCalled)
	}
	if !ovmfSecure {
		t.Error("windows-11 should probe for a secboot OVMF build")
	}

	// A plain Linux spec must probe nothing.
	swtpmCalled, ovmfCalled, isoCalled = false, false, false
	if err := probeUEFIRequirements(types.VMSpec{Name: "linux"}); err != nil {
		t.Fatalf("probeUEFIRequirements(linux): %v", err)
	}
	if swtpmCalled || ovmfCalled || isoCalled {
		t.Errorf("linux spec should not probe, got swtpm:%t ovmf:%t iso:%t", swtpmCalled, ovmfCalled, isoCalled)
	}
}

func TestProbeInstallISO_MissingFile(t *testing.T) {
	err := probeInstallISO("/definitely/not/here.iso")
	if err == nil {
		t.Fatal("expected error for missing ISO")
	}
	apiErr, ok := err.(*types.APIError)
	if !ok || apiErr.Code != "invalid_install_iso" {
		t.Fatalf("err = %v, want invalid_install_iso APIError", err)
	}
}
