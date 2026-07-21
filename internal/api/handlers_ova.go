package api

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/vmsmith/vmsmith/pkg/types"
)

// OVA import/export endpoints (roadmap 5.3).

// ExportVMOVA handles GET /api/v1/vms/{vmID}/export/ova.
//
// The VM must be stopped so the disk is quiescent — a running VM returns
// 409 vm_running. The OVA (OVF descriptor + streamOptimized VMDK + SHA256
// manifest) is assembled server-side and streamed as a tar download.
func (s *Server) ExportVMOVA(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "vmID")
	vm, err := s.vmManager.Get(r.Context(), id)
	if err != nil {
		apiErr := sanitizeManagerError(err)
		writeAPIError(w, statusForAPIError(apiErr, http.StatusNotFound), apiErr)
		return
	}
	if vm.State != types.VMStateStopped {
		writeAPIError(w, http.StatusConflict, types.NewAPIError("vm_running", "vm must be stopped before exporting an OVA"))
		return
	}

	workDir, err := os.MkdirTemp("", "vmsmith-ova-*")
	if err != nil {
		writeErrorCode(w, http.StatusInternalServerError, "export_failed", "creating export workspace: "+err.Error())
		return
	}
	defer os.RemoveAll(workDir)

	ovaPath := filepath.Join(workDir, vm.Name+".ova")
	if err := s.storageMgr.ExportOVA(vm, ovaPath, nil); err != nil {
		writeErrorCode(w, http.StatusInternalServerError, "export_failed", "exporting OVA: "+err.Error())
		return
	}

	s.publishAppEvent("vm.exported", vm.ID, fmt.Sprintf("VM %q exported as OVA", vm.Name), map[string]string{
		"name":   vm.Name,
		"format": "ova",
	})

	w.Header().Set("Content-Type", "application/x-tar")
	w.Header().Set("Content-Disposition", "attachment; filename="+vm.Name+".ova")
	http.ServeFile(w, r, ovaPath)
}

// ImportVMOVA handles POST /api/v1/vms/import/ova.
//
// Multipart form: `file` (the .ova), optional `name` (VM name; defaults to
// the descriptor's VirtualSystem name), optional `ssh_pub_key` and
// `default_user` passed through to the created VM. The appliance disk is
// converted to qcow2 and registered as an image named `<vm-name>-ova`,
// and a stopped VM is created with the descriptor's CPU/RAM/disk sizing
// (falling back to configured defaults when the descriptor omits them).
func (s *Server) ImportVMOVA(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseMultipartForm(32 << 20); err != nil {
		if isRequestTooLarge(err) {
			writeErrorCode(w, http.StatusRequestEntityTooLarge, "request_too_large", "upload body too large")
			return
		}
		writeErrorCode(w, http.StatusBadRequest, "invalid_multipart_form", "failed to parse form: "+err.Error())
		return
	}

	file, header, err := r.FormFile("file")
	if err != nil {
		writeErrorCode(w, http.StatusBadRequest, "missing_file", "missing file field")
		return
	}
	defer file.Close()

	lowerName := strings.ToLower(strings.TrimSpace(header.Filename))
	if !strings.HasSuffix(lowerName, ".ova") {
		writeAPIError(w, http.StatusBadRequest, types.NewAPIError("invalid_ova", "uploaded file must have a .ova extension"))
		return
	}

	// Spool beside the durable images store rather than /tmp, which is often
	// tmpfs and can fill up on multi-GB appliance uploads.
	spoolDir := filepath.Dir(s.storageMgr.ImagePath(".ova-upload-spool"))
	if err := os.MkdirAll(spoolDir, 0o755); err != nil {
		writeErrorCode(w, http.StatusInternalServerError, "import_failed", "creating upload spool dir: "+err.Error())
		return
	}
	spool, err := os.CreateTemp(spoolDir, "vmsmith-ova-import-*.ova")
	if err != nil {
		writeErrorCode(w, http.StatusInternalServerError, "import_failed", "creating upload spool: "+err.Error())
		return
	}
	defer os.Remove(spool.Name())
	if _, err := io.Copy(spool, file); err != nil {
		spool.Close()
		writeErrorCode(w, http.StatusInternalServerError, "upload_read_failed", "reading upload: "+err.Error())
		return
	}
	spool.Close()

	// Peek at the descriptor first so an omitted `name` can fall back to the
	// appliance's own name before we commit to an image name.
	vmName := strings.TrimSpace(r.FormValue("name"))
	imageName := strings.TrimSpace(r.FormValue("image_name"))

	result, err := s.storageMgr.ImportOVA(spool.Name(), importImageName(imageName, vmName, header.Filename))
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, types.NewAPIError("invalid_ova", "importing OVA: "+err.Error()))
		return
	}

	if vmName == "" {
		vmName = result.Name
	}

	spec := types.VMSpec{
		Name:        vmName,
		Image:       result.Image.Name,
		CPUs:        result.CPUs,
		RAMMB:       result.RAMMB,
		DiskGB:      result.DiskGB,
		SSHPubKey:   strings.TrimSpace(r.FormValue("ssh_pub_key")),
		DefaultUser: strings.TrimSpace(r.FormValue("default_user")),
		Description: fmt.Sprintf("Imported from %s", filepath.Base(header.Filename)),
	}
	// Zero CPU/RAM/disk values are legal: the VM manager falls back to the
	// daemon's configured defaults exactly as POST /vms does.

	cleanupImage := func() {
		_ = s.storageMgr.DeleteImage(result.Image.ID)
	}

	if err := validateVMSpec(spec); err != nil {
		cleanupImage()
		writeAPIError(w, http.StatusBadRequest, err)
		return
	}

	existingVMs, err := s.vmManager.List(r.Context())
	if err != nil {
		cleanupImage()
		err = sanitizeManagerError(err)
		writeAPIError(w, statusForAPIError(err, http.StatusInternalServerError), err)
		return
	}
	if err := validateUniqueVMName(spec.Name, existingVMs); err != nil {
		cleanupImage()
		writeAPIError(w, http.StatusBadRequest, err)
		return
	}

	if err := s.enforceCreateQuotas(r.Context(), spec); err != nil {
		cleanupImage()
		err = sanitizeManagerError(err)
		writeAPIError(w, statusForAPIError(err, http.StatusInternalServerError), err)
		return
	}

	release, ok := s.acquireCreateSlot()
	if !ok {
		cleanupImage()
		writeAPIError(w, http.StatusTooManyRequests, types.NewAPIError("create_limit_reached", "too many VM create operations in progress; retry once an existing create finishes"))
		return
	}
	if release != nil {
		defer release()
	}

	vm, err := s.vmManager.Create(r.Context(), spec)
	if err != nil {
		cleanupImage()
		err = logAndSanitizeManagerError("import ova", err)
		writeAPIError(w, statusForAPIError(err, http.StatusInternalServerError), err)
		return
	}

	s.publishAppEvent("vm.imported", vm.ID, fmt.Sprintf("VM %q imported from OVA", vm.Name), map[string]string{
		"name":  vm.Name,
		"image": result.Image.Name,
	})

	writeJSON(w, http.StatusCreated, vm.RedactConsoleSecrets())
}

// importImageName picks the image name for an OVA import: explicit
// `image_name` wins, then `<vm-name>-ova`, then the OVA filename stem.
func importImageName(imageName, vmName, filename string) string {
	if imageName != "" {
		return imageName
	}
	if vmName != "" {
		return vmName + "-ova"
	}
	stem := strings.TrimSuffix(filepath.Base(filename), filepath.Ext(filename))
	return strings.TrimSpace(stem) + "-ova"
}
