package api

import (
	"net/http"
	"testing"

	"github.com/vmsmith/vmsmith/pkg/types"
)

func patchVM(t *testing.T, url string, body any) *http.Response {
	t.Helper()
	req, _ := http.NewRequest(http.MethodPatch, url, jsonBody(t, body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("PATCH %s: %v", url, err)
	}
	return resp
}

func TestCreateVM_LinuxInstallISO(t *testing.T) {
	ts, _, cleanup := testServer(t)
	defer cleanup()

	resp, err := http.Post(ts.URL+"/api/v1/vms", "application/json", jsonBody(t, map[string]any{
		"name":        "umbrel",
		"install_iso": "umbrelos-amd64-usb-installer.iso",
		"firmware":    "uefi",
		"secure_boot": false,
		"disk_gb":     128,
	}))
	if err != nil {
		t.Fatalf("POST /vms: %v", err)
	}
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("status = %d, want 201", resp.StatusCode)
	}
	var created types.VM
	decodeJSON(t, resp, &created)
	if created.Spec.Image != "" {
		t.Errorf("Spec.Image = %q, want empty (blank-disk install)", created.Spec.Image)
	}
	if want := "/var/lib/vmsmith/images/umbrelos-amd64-usb-installer.iso"; created.Spec.InstallISO != want {
		t.Errorf("Spec.InstallISO = %q, want %q (bare names resolve against the images dir)", created.Spec.InstallISO, want)
	}
	if created.Spec.SecureBoot == nil || *created.Spec.SecureBoot {
		t.Errorf("Spec.SecureBoot = %v, want explicit false", created.Spec.SecureBoot)
	}
}

func TestCreateVM_InstallISOIgnoresTemplateImage(t *testing.T) {
	ts, _, cleanup := testServer(t)
	defer cleanup()

	templateResp, err := http.Post(ts.URL+"/api/v1/templates", "application/json", jsonBody(t, map[string]any{
		"name": "sized", "image": "ubuntu.qcow2", "cpus": 4, "ram_mb": 8192, "disk_gb": 64,
	}))
	if err != nil {
		t.Fatalf("POST /templates: %v", err)
	}
	var tpl types.VMTemplate
	decodeJSON(t, templateResp, &tpl)

	resp, err := http.Post(ts.URL+"/api/v1/vms", "application/json", jsonBody(t, map[string]any{
		"name": "iso-from-tpl", "template_id": tpl.ID, "install_iso": "/isos/debian.iso",
	}))
	if err != nil {
		t.Fatalf("POST /vms: %v", err)
	}
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("status = %d, want 201 (install_iso must win over the template image)", resp.StatusCode)
	}
	var created types.VM
	decodeJSON(t, resp, &created)
	if created.Spec.Image != "" || created.Spec.CPUs != 4 || created.Spec.DiskGB != 64 {
		t.Errorf("spec = %+v, want template sizing without its image", created.Spec)
	}
}

func TestUpdateVM_EjectInstallISO(t *testing.T) {
	ts, mockMgr, cleanup := testServer(t)
	defer cleanup()

	mockMgr.SeedVM(&types.VM{
		ID: "vm-iso", Name: "installer",
		Spec: types.VMSpec{CPUs: 2, RAMMB: 4096, DiskGB: 64, InstallISO: "/isos/debian.iso"},
	})

	resp := patchVM(t, ts.URL+"/api/v1/vms/vm-iso", map[string]any{"install_iso": "/isos/other.iso"})
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("attach on PATCH: status = %d, want 400", resp.StatusCode)
	}
	resp.Body.Close()

	resp = patchVM(t, ts.URL+"/api/v1/vms/vm-iso", map[string]any{"install_iso": ""})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("eject: status = %d, want 200", resp.StatusCode)
	}
	var updated types.VM
	decodeJSON(t, resp, &updated)
	if updated.Spec.InstallISO != "" {
		t.Errorf("Spec.InstallISO = %q after eject, want empty", updated.Spec.InstallISO)
	}
	if updated.Spec.CPUs != 2 || updated.Spec.DiskGB != 64 {
		t.Errorf("eject changed unrelated fields: %+v", updated.Spec)
	}
}
