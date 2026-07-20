package api

import (
	"archive/tar"
	"bytes"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vmsmith/vmsmith/pkg/types"
)

// installFakeQemuImgAPI prepends a fake qemu-img (convert = copy) to PATH.
func installFakeQemuImgAPI(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	script := `#!/bin/sh
dst=$(eval echo \${$#})
src=$(eval echo \${$(($# - 1))})
cp "$src" "$dst"
`
	if err := os.WriteFile(filepath.Join(dir, "qemu-img"), []byte(script), 0o755); err != nil {
		t.Fatalf("writing fake qemu-img: %v", err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

// buildTestOVA crafts a minimal valid OVA in-memory.
func buildTestOVA(t *testing.T, name string, cpus, ramMB, diskGB int) []byte {
	t.Helper()
	descriptor := `<?xml version="1.0"?>
<Envelope xmlns="http://schemas.dmtf.org/ovf/envelope/1" xmlns:ovf="http://schemas.dmtf.org/ovf/envelope/1">
  <References><File ovf:href="disk1.vmdk" ovf:id="file1"/></References>
  <DiskSection><Info/><Disk ovf:capacity="` + itoa(diskGB) + `" ovf:capacityAllocationUnits="byte * 2^30" ovf:diskId="vmdisk1" ovf:fileRef="file1"/></DiskSection>
  <NetworkSection><Info/><Network ovf:name="nat"/></NetworkSection>
  <VirtualSystem ovf:id="` + name + `">
    <Info/><Name>` + name + `</Name>
    <VirtualHardwareSection>
      <Info/>
      <Item><ResourceType>3</ResourceType><VirtualQuantity>` + itoa(cpus) + `</VirtualQuantity></Item>
      <Item><ResourceType>4</ResourceType><VirtualQuantity>` + itoa(ramMB) + `</VirtualQuantity><AllocationUnits>byte * 2^20</AllocationUnits></Item>
    </VirtualHardwareSection>
  </VirtualSystem>
</Envelope>`

	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	if err := tw.WriteHeader(&tar.Header{Name: name + ".ovf", Mode: 0o644, Size: int64(len(descriptor))}); err != nil {
		t.Fatal(err)
	}
	tw.Write([]byte(descriptor))
	disk := []byte("fake-vmdk-bytes")
	tw.WriteHeader(&tar.Header{Name: "disk1.vmdk", Mode: 0o644, Size: int64(len(disk))})
	tw.Write(disk)
	tw.Close()
	return buf.Bytes()
}

func postOVA(t *testing.T, url string, filename string, ova []byte, fields map[string]string) *http.Response {
	t.Helper()
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	fw, err := mw.CreateFormFile("file", filename)
	if err != nil {
		t.Fatal(err)
	}
	fw.Write(ova)
	for k, v := range fields {
		mw.WriteField(k, v)
	}
	mw.Close()

	resp, err := http.Post(url+"/api/v1/vms/import/ova", mw.FormDataContentType(), &body)
	if err != nil {
		t.Fatalf("POST import: %v", err)
	}
	return resp
}

func TestImportVMOVA_CreatesVMAndImage(t *testing.T) {
	installFakeQemuImgAPI(t)
	ts, mockMgr, cleanup := testServer(t)
	defer cleanup()

	ova := buildTestOVA(t, "appliance", 2, 4096, 20)
	resp := postOVA(t, ts.URL, "appliance.ova", ova, map[string]string{"name": "imported-vm"})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, body = %s", resp.StatusCode, body)
	}

	var vmResp types.VM
	if err := json.NewDecoder(resp.Body).Decode(&vmResp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if vmResp.Name != "imported-vm" {
		t.Errorf("Name = %q, want imported-vm", vmResp.Name)
	}
	if vmResp.Spec.CPUs != 2 || vmResp.Spec.RAMMB != 4096 || vmResp.Spec.DiskGB != 20 {
		t.Errorf("spec = %+v, want 2/4096/20", vmResp.Spec)
	}
	if vmResp.Spec.Image != "imported-vm-ova" {
		t.Errorf("Image = %q, want imported-vm-ova", vmResp.Spec.Image)
	}
	if mockMgr.VMCount() != 1 {
		t.Errorf("VMCount = %d, want 1", mockMgr.VMCount())
	}

	// The converted image must be registered and listable.
	imgResp, err := http.Get(ts.URL + "/api/v1/images")
	if err != nil {
		t.Fatal(err)
	}
	defer imgResp.Body.Close()
	var images []types.Image
	json.NewDecoder(imgResp.Body).Decode(&images)
	found := false
	for _, img := range images {
		if img.Name == "imported-vm-ova" {
			found = true
		}
	}
	if !found {
		t.Errorf("imported image not listed: %+v", images)
	}
}

func TestImportVMOVA_NameDefaultsToDescriptor(t *testing.T) {
	installFakeQemuImgAPI(t)
	ts, _, cleanup := testServer(t)
	defer cleanup()

	ova := buildTestOVA(t, "descriptor-name", 1, 1024, 10)
	resp := postOVA(t, ts.URL, "upload.ova", ova, nil)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, body = %s", resp.StatusCode, body)
	}
	var vmResp types.VM
	json.NewDecoder(resp.Body).Decode(&vmResp)
	if vmResp.Name != "descriptor-name" {
		t.Errorf("Name = %q, want descriptor-name", vmResp.Name)
	}
}

func TestImportVMOVA_RejectsNonOVAExtension(t *testing.T) {
	ts, _, cleanup := testServer(t)
	defer cleanup()

	resp := postOVA(t, ts.URL, "appliance.qcow2", []byte("not-a-tar"), nil)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
	assertAPIErrorCode(t, resp, "invalid_ova")
}

func TestImportVMOVA_RejectsGarbageArchive(t *testing.T) {
	installFakeQemuImgAPI(t)
	ts, _, cleanup := testServer(t)
	defer cleanup()

	resp := postOVA(t, ts.URL, "garbage.ova", []byte("definitely-not-a-tar-archive"), nil)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
	assertAPIErrorCode(t, resp, "invalid_ova")
}

func TestImportVMOVA_DuplicateVMNameRejectedAndImageCleanedUp(t *testing.T) {
	installFakeQemuImgAPI(t)
	ts, mockMgr, cleanup := testServer(t)
	defer cleanup()

	mockMgr.SeedVM(&types.VM{ID: "vm-1", Name: "taken", State: types.VMStateStopped})

	ova := buildTestOVA(t, "appliance", 1, 1024, 10)
	resp := postOVA(t, ts.URL, "appliance.ova", ova, map[string]string{"name": "taken"})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, body = %s", resp.StatusCode, body)
	}

	// The provisionally imported image must have been rolled back.
	imgResp, err := http.Get(ts.URL + "/api/v1/images")
	if err != nil {
		t.Fatal(err)
	}
	defer imgResp.Body.Close()
	var images []types.Image
	json.NewDecoder(imgResp.Body).Decode(&images)
	for _, img := range images {
		if strings.Contains(img.Name, "taken") {
			t.Errorf("orphaned image left behind: %+v", img)
		}
	}
}

func TestExportVMOVA_StreamsTarForStoppedVM(t *testing.T) {
	installFakeQemuImgAPI(t)
	ts, mockMgr, cleanup := testServer(t)
	defer cleanup()

	diskPath := filepath.Join(t.TempDir(), "disk.qcow2")
	os.WriteFile(diskPath, []byte("disk-bytes"), 0o644)
	mockMgr.SeedVM(&types.VM{
		ID: "vm-exp", Name: "exportable", State: types.VMStateStopped,
		DiskPath: diskPath,
		Spec:     types.VMSpec{Name: "exportable", CPUs: 2, RAMMB: 2048, DiskGB: 20},
	})

	resp, err := http.Get(ts.URL + "/api/v1/vms/vm-exp/export/ova")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, body = %s", resp.StatusCode, body)
	}
	if cd := resp.Header.Get("Content-Disposition"); !strings.Contains(cd, "exportable.ova") {
		t.Errorf("Content-Disposition = %q", cd)
	}

	tr := tar.NewReader(resp.Body)
	var names []string
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("tar: %v", err)
		}
		names = append(names, hdr.Name)
	}
	if len(names) != 3 || names[0] != "exportable.ovf" {
		t.Fatalf("tar entries = %v, want [exportable.ovf exportable-disk1.vmdk exportable.mf]", names)
	}
}

func TestExportVMOVA_RunningVMConflict(t *testing.T) {
	ts, mockMgr, cleanup := testServer(t)
	defer cleanup()

	mockMgr.SeedVM(&types.VM{ID: "vm-run", Name: "running", State: types.VMStateRunning})

	resp, err := http.Get(ts.URL + "/api/v1/vms/vm-run/export/ova")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("status = %d, want 409", resp.StatusCode)
	}
	assertAPIErrorCode(t, resp, "vm_running")
}

func TestExportVMOVA_UnknownVMNotFound(t *testing.T) {
	ts, _, cleanup := testServer(t)
	defer cleanup()

	resp, err := http.Get(ts.URL + "/api/v1/vms/vm-missing/export/ova")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", resp.StatusCode)
	}
}
