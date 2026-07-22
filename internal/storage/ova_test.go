package storage

import (
	"archive/tar"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vmsmith/vmsmith/internal/config"
	"github.com/vmsmith/vmsmith/internal/store"
	"github.com/vmsmith/vmsmith/pkg/types"
)

// installFakeQemuImg prepends a directory to PATH containing a qemu-img
// stand-in whose convert subcommand simply copies source to destination.
func installFakeQemuImg(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	script := `#!/bin/sh
# fake qemu-img: copy the second-to-last argument to the last argument
dst=$(eval echo \${$#})
src=$(eval echo \${$(($# - 1))})
cp "$src" "$dst"
`
	if err := os.WriteFile(filepath.Join(dir, "qemu-img"), []byte(script), 0o755); err != nil {
		t.Fatalf("writing fake qemu-img: %v", err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func ovaTestManager(t *testing.T) *Manager {
	t.Helper()
	dir := t.TempDir()
	s, err := store.New(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	t.Cleanup(func() { s.Close() })

	cfg := config.DefaultConfig()
	cfg.Storage.ImagesDir = filepath.Join(dir, "images")
	cfg.Storage.BaseDir = filepath.Join(dir, "vms")
	os.MkdirAll(cfg.Storage.ImagesDir, 0o755)
	os.MkdirAll(cfg.Storage.BaseDir, 0o755)
	return NewManager(cfg, s)
}

func exportTestVM(t *testing.T) *types.VM {
	t.Helper()
	diskDir := t.TempDir()
	diskPath := filepath.Join(diskDir, "disk.qcow2")
	if err := os.WriteFile(diskPath, []byte("fake-qcow2-disk-bytes"), 0o644); err != nil {
		t.Fatalf("writing fake disk: %v", err)
	}
	return &types.VM{
		ID:       "vm-1",
		Name:     "appliance",
		State:    types.VMStateStopped,
		DiskPath: diskPath,
		Spec:     types.VMSpec{Name: "appliance", CPUs: 4, RAMMB: 8192, DiskGB: 40},
	}
}

func readTarEntries(t *testing.T, path string) []string {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open ova: %v", err)
	}
	defer f.Close()
	var names []string
	tr := tar.NewReader(f)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("read tar: %v", err)
		}
		names = append(names, hdr.Name)
	}
	return names
}

func TestBuildOVFDescriptor_RoundTripsThroughParser(t *testing.T) {
	vm := exportTestVM(t)
	descriptor, err := buildOVFDescriptor(vm, "appliance-disk1.vmdk", 1234)
	if err != nil {
		t.Fatalf("buildOVFDescriptor: %v", err)
	}

	parsed, diskHref, err := parseOVFDescriptor(descriptor)
	if err != nil {
		t.Fatalf("parseOVFDescriptor: %v", err)
	}
	if parsed.Name != "appliance" {
		t.Errorf("Name = %q, want appliance", parsed.Name)
	}
	if parsed.CPUs != 4 {
		t.Errorf("CPUs = %d, want 4", parsed.CPUs)
	}
	if parsed.RAMMB != 8192 {
		t.Errorf("RAMMB = %d, want 8192", parsed.RAMMB)
	}
	if parsed.DiskGB != 40 {
		t.Errorf("DiskGB = %d, want 40", parsed.DiskGB)
	}
	if diskHref != "appliance-disk1.vmdk" {
		t.Errorf("diskHref = %q, want appliance-disk1.vmdk", diskHref)
	}
}

func TestExportOVA_DescriptorIsFirstTarEntry(t *testing.T) {
	installFakeQemuImg(t)
	m := ovaTestManager(t)
	vm := exportTestVM(t)

	out := filepath.Join(t.TempDir(), "appliance.ova")
	if err := m.ExportOVA(vm, out, nil); err != nil {
		t.Fatalf("ExportOVA: %v", err)
	}

	entries := readTarEntries(t, out)
	want := []string{"appliance.ovf", "appliance-disk1.vmdk", "appliance.mf"}
	if len(entries) != len(want) {
		t.Fatalf("entries = %v, want %v", entries, want)
	}
	for i := range want {
		if entries[i] != want[i] {
			t.Fatalf("entries = %v, want %v (descriptor must be first)", entries, want)
		}
	}
}

func TestExportOVA_ManifestCarriesSHA256Lines(t *testing.T) {
	installFakeQemuImg(t)
	m := ovaTestManager(t)
	vm := exportTestVM(t)

	out := filepath.Join(t.TempDir(), "appliance.ova")
	if err := m.ExportOVA(vm, out, nil); err != nil {
		t.Fatalf("ExportOVA: %v", err)
	}

	f, err := os.Open(out)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer f.Close()
	tr := tar.NewReader(f)
	var manifest string
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("tar: %v", err)
		}
		if strings.HasSuffix(hdr.Name, ".mf") {
			data, _ := io.ReadAll(tr)
			manifest = string(data)
		}
	}
	if !strings.Contains(manifest, "SHA256(appliance.ovf)= ") ||
		!strings.Contains(manifest, "SHA256(appliance-disk1.vmdk)= ") {
		t.Fatalf("manifest missing SHA256 lines:\n%s", manifest)
	}
}

func TestExportOVA_MissingDiskFails(t *testing.T) {
	installFakeQemuImg(t)
	m := ovaTestManager(t)
	vm := exportTestVM(t)
	vm.DiskPath = filepath.Join(t.TempDir(), "nope.qcow2")

	if err := m.ExportOVA(vm, filepath.Join(t.TempDir(), "x.ova"), nil); err == nil {
		t.Fatal("expected error for missing disk")
	}
}

// exportForImport produces a real OVA via the exporter for import tests.
func exportForImport(t *testing.T, m *Manager) string {
	t.Helper()
	vm := exportTestVM(t)
	out := filepath.Join(t.TempDir(), "appliance.ova")
	if err := m.ExportOVA(vm, out, nil); err != nil {
		t.Fatalf("ExportOVA: %v", err)
	}
	return out
}

func TestImportOVA_RegistersImageAndParsesSpecs(t *testing.T) {
	installFakeQemuImg(t)
	m := ovaTestManager(t)
	ova := exportForImport(t, m)

	result, err := m.ImportOVA(ova, "imported-appliance")
	if err != nil {
		t.Fatalf("ImportOVA: %v", err)
	}
	if result.Name != "appliance" {
		t.Errorf("Name = %q, want appliance", result.Name)
	}
	if result.CPUs != 4 || result.RAMMB != 8192 || result.DiskGB != 40 {
		t.Errorf("parsed specs = %d/%d/%d, want 4/8192/40", result.CPUs, result.RAMMB, result.DiskGB)
	}
	if result.Image == nil || result.Image.Name != "imported-appliance" {
		t.Fatalf("image not registered: %+v", result.Image)
	}
	if _, err := os.Stat(result.Image.Path); err != nil {
		t.Fatalf("converted qcow2 missing: %v", err)
	}
	if !strings.HasSuffix(result.Image.Path, "imported-appliance.qcow2") {
		t.Errorf("image path = %q", result.Image.Path)
	}
}

func TestImportOVA_DuplicateImageNameRejected(t *testing.T) {
	installFakeQemuImg(t)
	m := ovaTestManager(t)
	ova := exportForImport(t, m)

	if _, err := m.ImportOVA(ova, "dup"); err != nil {
		t.Fatalf("first import: %v", err)
	}
	if _, err := m.ImportOVA(ova, "dup"); err == nil {
		t.Fatal("expected duplicate-image error")
	}
}

func TestImportOVA_MissingDescriptorRejected(t *testing.T) {
	installFakeQemuImg(t)
	m := ovaTestManager(t)

	// Tar with only a disk entry, no .ovf.
	path := filepath.Join(t.TempDir(), "bad.ova")
	f, _ := os.Create(path)
	tw := tar.NewWriter(f)
	tw.WriteHeader(&tar.Header{Name: "disk.vmdk", Mode: 0o644, Size: 4})
	tw.Write([]byte("disk"))
	tw.Close()
	f.Close()

	if _, err := m.ImportOVA(path, "x"); err == nil || !strings.Contains(err.Error(), "no .ovf descriptor") {
		t.Fatalf("err = %v, want no-descriptor error", err)
	}
}

func TestImportOVA_TraversalDiskHrefRejected(t *testing.T) {
	installFakeQemuImg(t)
	m := ovaTestManager(t)

	descriptor := `<?xml version="1.0"?>
<Envelope xmlns="http://schemas.dmtf.org/ovf/envelope/1">
  <References><File ovf:href="../../etc/passwd" ovf:id="file1" xmlns:ovf="http://schemas.dmtf.org/ovf/envelope/1"/></References>
  <DiskSection><Info/><Disk ovf:capacity="1" ovf:diskId="vmdisk1" ovf:fileRef="file1" xmlns:ovf="http://schemas.dmtf.org/ovf/envelope/1"/></DiskSection>
  <NetworkSection><Info/></NetworkSection>
  <VirtualSystem ovf:id="evil" xmlns:ovf="http://schemas.dmtf.org/ovf/envelope/1"><Info/><VirtualHardwareSection><Info/></VirtualHardwareSection></VirtualSystem>
</Envelope>`

	path := filepath.Join(t.TempDir(), "evil.ova")
	f, _ := os.Create(path)
	tw := tar.NewWriter(f)
	tw.WriteHeader(&tar.Header{Name: "evil.ovf", Mode: 0o644, Size: int64(len(descriptor))})
	tw.Write([]byte(descriptor))
	tw.Close()
	f.Close()

	if _, err := m.ImportOVA(path, "x"); err == nil || !strings.Contains(err.Error(), "not a safe relative path") {
		t.Fatalf("err = %v, want traversal rejection", err)
	}
}

func TestImportOVA_DiskHrefWithDoubleDotInFilenameAllowed(t *testing.T) {
	installFakeQemuImg(t)
	m := ovaTestManager(t)

	dir := t.TempDir()
	descriptor := `<?xml version="1.0"?>
<Envelope xmlns="http://schemas.dmtf.org/ovf/envelope/1" xmlns:ovf="http://schemas.dmtf.org/ovf/envelope/1">
  <References><File ovf:href="disk..bak.vmdk" ovf:id="file1"/></References>
  <DiskSection><Info/><Disk ovf:capacity="1" ovf:diskId="vmdisk1" ovf:fileRef="file1"/></DiskSection>
  <VirtualSystem ovf:id="ok"><Info/><VirtualHardwareSection><Info/></VirtualHardwareSection></VirtualSystem>
</Envelope>`
	if err := os.WriteFile(filepath.Join(dir, "machine.ovf"), []byte(descriptor), 0o644); err != nil {
		t.Fatalf("write ovf: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "disk..bak.vmdk"), []byte("disk"), 0o644); err != nil {
		t.Fatalf("write disk: %v", err)
	}

	if _, err := m.ImportOVA(filepath.Join(dir, "machine.ovf"), "dotdot-ok"); err != nil {
		t.Fatalf("ImportOVA: %v", err)
	}
}

func TestParseOVFDescriptor_DiskSectionMissingDoesNotGrabFirstReference(t *testing.T) {
	descriptor := []byte(`<?xml version="1.0"?>
<Envelope xmlns="http://schemas.dmtf.org/ovf/envelope/1" xmlns:ovf="http://schemas.dmtf.org/ovf/envelope/1">
  <References>
    <File ovf:href="manifest.mf" ovf:id="manifest"/>
    <File ovf:href="disk1.vmdk" ovf:id="disk1"/>
  </References>
  <VirtualSystem ovf:id="appliance"><Info/><VirtualHardwareSection><Info/></VirtualHardwareSection></VirtualSystem>
</Envelope>`)

	parsed, diskHref, err := parseOVFDescriptor(descriptor)
	if err != nil {
		t.Fatalf("parseOVFDescriptor: %v", err)
	}
	if parsed.Name != "appliance" {
		t.Fatalf("Name = %q, want appliance", parsed.Name)
	}
	if diskHref != "" {
		t.Fatalf("diskHref = %q, want empty when DiskSection is absent and references are ambiguous", diskHref)
	}
}

func TestImportOVA_BareOVFWithSiblingDisk(t *testing.T) {
	installFakeQemuImg(t)
	m := ovaTestManager(t)

	dir := t.TempDir()
	vm := exportTestVM(t)
	descriptor, err := buildOVFDescriptor(vm, "disk1.vmdk", 4)
	if err != nil {
		t.Fatalf("descriptor: %v", err)
	}
	os.WriteFile(filepath.Join(dir, "machine.ovf"), descriptor, 0o644)
	os.WriteFile(filepath.Join(dir, "disk1.vmdk"), []byte("disk"), 0o644)

	result, err := m.ImportOVA(filepath.Join(dir, "machine.ovf"), "from-ovf")
	if err != nil {
		t.Fatalf("ImportOVA: %v", err)
	}
	if result.CPUs != 4 || result.Image == nil {
		t.Fatalf("unexpected result: %+v", result)
	}
}

func TestMemoryToMB_Units(t *testing.T) {
	cases := []struct {
		quantity int64
		units    string
		want     int64
	}{
		{2048, "byte * 2^20", 2048},
		{2, "byte * 2^30", 2048},
		{2097152, "byte * 2^10", 2048},
		{2147483648, "byte", 2048},
		{4096, "", 4096},
	}
	for _, tc := range cases {
		if got := memoryToMB(tc.quantity, tc.units); got != tc.want {
			t.Errorf("memoryToMB(%d, %q) = %d, want %d", tc.quantity, tc.units, got, tc.want)
		}
	}
}

func TestCapacityToGB_Units(t *testing.T) {
	cases := []struct {
		capacity int64
		units    string
		want     int64
	}{
		{40, "byte * 2^30", 40},
		{40960, "byte * 2^20", 40},
		{1, "byte", 1},             // rounds up to 1 GB minimum
		{3 << 30, "", 3},           // bare bytes
		{(3 << 30) + 1, "byte", 4}, // partial GB rounds up
		{0, "byte * 2^30", 1},      // floor of 1 GB
	}
	for _, tc := range cases {
		if got := capacityToGB(tc.capacity, tc.units); got != tc.want {
			t.Errorf("capacityToGB(%d, %q) = %d, want %d", tc.capacity, tc.units, got, tc.want)
		}
	}
}

func TestOVAWorkRoot_PrivateAndUnderBaseDir(t *testing.T) {
	m := ovaTestManager(t)
	root, err := m.OVAWorkRoot()
	if err != nil {
		t.Fatalf("OVAWorkRoot: %v", err)
	}
	if filepath.Dir(root) != m.cfg.Storage.BaseDir {
		t.Fatalf("work root %q not directly under base dir %q", root, m.cfg.Storage.BaseDir)
	}
	info, err := os.Stat(root)
	if err != nil {
		t.Fatalf("stat work root: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o700 {
		t.Fatalf("work root perm = %o, want 0700 (transient appliance payloads must not be world-readable)", perm)
	}
}

func TestSweepStaleOVAWork_RemovesLeftovers(t *testing.T) {
	m := ovaTestManager(t)

	// No work root yet: sweep is a silent no-op.
	if removed, err := m.SweepStaleOVAWork(); err != nil || removed != 0 {
		t.Fatalf("sweep on missing root = (%d, %v), want (0, nil)", removed, err)
	}

	root, err := m.OVAWorkRoot()
	if err != nil {
		t.Fatalf("OVAWorkRoot: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(root, "import-stale", "nested"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "upload-stale.ova"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}

	removed, err := m.SweepStaleOVAWork()
	if err != nil {
		t.Fatalf("sweep: %v", err)
	}
	if removed != 2 {
		t.Fatalf("removed = %d, want 2", removed)
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatalf("readdir after sweep: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("work root not empty after sweep: %v", entries)
	}
}

func TestImportOVA_ConvertFailureLeavesNoOrphanImage(t *testing.T) {
	// Fake qemu-img that writes a partial destination file, then fails —
	// simulating disk-full / crash mid-convert.
	dir := t.TempDir()
	script := `#!/bin/sh
dst=$(eval echo \${$#})
echo partial > "$dst"
exit 1
`
	if err := os.WriteFile(filepath.Join(dir, "qemu-img"), []byte(script), 0o755); err != nil {
		t.Fatalf("writing fake qemu-img: %v", err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

	m := ovaTestManager(t)
	// Build the OVA with a working converter first, then swap in the failing one.
	installFakeQemuImg(t)
	ova := exportForImport(t, m)
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

	if _, err := m.ImportOVA(ova, "convert-fails"); err == nil {
		t.Fatal("expected convert failure error")
	}
	if _, err := os.Stat(m.ImagePath("convert-fails")); !os.IsNotExist(err) {
		t.Fatalf("partial image left behind after failed convert (stat err = %v)", err)
	}

	// A retry must not trip the "image already exists" guard.
	installFakeQemuImg(t)
	if _, err := m.ImportOVA(ova, "convert-fails"); err != nil {
		t.Fatalf("retry after failed convert: %v", err)
	}
}
