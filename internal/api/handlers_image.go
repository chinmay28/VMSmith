package api

import (
	"encoding/json"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/go-chi/chi/v5"
	"github.com/vmsmith/vmsmith/pkg/types"
)

type createImageRequest struct {
	VMID string `json:"vm_id"`
	Name string `json:"name"`
}

// CreateImage handles POST /api/v1/images
func (s *Server) CreateImage(w http.ResponseWriter, r *http.Request) {
	var req createImageRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		if isRequestTooLarge(err) {
			writeError(w, http.StatusRequestEntityTooLarge, "request body too large")
			return
		}
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if err := validateCreateImageRequest(req.VMID, req.Name); err != nil {
		writeAPIError(w, http.StatusBadRequest, err)
		return
	}

	// Get the VM to find its disk path
	vm, err := s.vmManager.Get(r.Context(), req.VMID)
	if err != nil {
		apiErr := sanitizeManagerError(err)
		writeAPIError(w, statusForAPIError(apiErr, http.StatusInternalServerError), apiErr)
		return
	}

	img, err := s.storageMgr.CreateImage(vm.DiskPath, req.Name, vm.ID)
	if err != nil {
		apiErr := sanitizeManagerError(err)
		writeAPIError(w, statusForAPIError(apiErr, http.StatusInternalServerError), apiErr)
		return
	}

	writeJSON(w, http.StatusCreated, img)
}

// ListImages handles GET /api/v1/images
func (s *Server) ListImages(w http.ResponseWriter, r *http.Request) {
	imgs, err := s.storageMgr.ListImages()
	if err != nil {
		apiErr := sanitizeManagerError(err)
		writeAPIError(w, statusForAPIError(apiErr, http.StatusInternalServerError), apiErr)
		return
	}

	total := len(imgs)
	pagination := parsePagination(r)
	imgs = paginateSlice(imgs, pagination.Page, pagination.PerPage)
	setTotalCountHeader(w, total)

	writeJSON(w, http.StatusOK, imgs)
}

// DeleteImage handles DELETE /api/v1/images/{imageID}
func (s *Server) DeleteImage(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "imageID")
	if err := s.storageMgr.DeleteImage(id); err != nil {
		apiErr := sanitizeManagerError(err)
		writeAPIError(w, statusForAPIError(apiErr, http.StatusInternalServerError), apiErr)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

var availableStorageBytes = func(path string) (uint64, error) {
	var stat syscall.Statfs_t
	if err := syscall.Statfs(path, &stat); err != nil {
		return 0, err
	}
	return stat.Bavail * uint64(stat.Bsize), nil
}

// UploadImage handles POST /api/v1/images/upload (multipart form: file + name)
func (s *Server) UploadImage(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseMultipartForm(32 << 20); err != nil {
		if isRequestTooLarge(err) {
			writeError(w, http.StatusRequestEntityTooLarge, "upload body too large")
			return
		}
		writeError(w, http.StatusBadRequest, "failed to parse form: "+err.Error())
		return
	}

	file, header, err := r.FormFile("file")
	if err != nil {
		writeError(w, http.StatusBadRequest, "missing file field")
		return
	}
	defer file.Close()

	name := strings.TrimSpace(r.FormValue("name"))
	if name == "" {
		// Derive name from filename, strip extension
		name = header.Filename
		if i := strings.LastIndex(name, "."); i > 0 {
			name = name[:i]
		}
	}
	if name == "" {
		writeAPIError(w, http.StatusBadRequest, types.NewAPIError("invalid_image", "image name is required"))
		return
	}

	data, err := io.ReadAll(file)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "reading upload: "+err.Error())
		return
	}
	if err := validateUploadedImage(header.Filename, data); err != nil {
		writeAPIError(w, http.StatusBadRequest, err)
		return
	}

	freeBytes, err := availableStorageBytes(filepath.Dir(s.storageMgr.ImagePath(name)))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "checking available storage: "+err.Error())
		return
	}
	if uint64(len(data)) > freeBytes {
		writeAPIError(w, http.StatusInsufficientStorage, types.NewAPIError("insufficient_storage", "not enough free disk space for uploaded image"))
		return
	}

	img, err := s.storageMgr.ImportImage(name, data)
	if err != nil {
		apiErr := sanitizeManagerError(err)
		writeAPIError(w, statusForAPIError(apiErr, http.StatusInternalServerError), apiErr)
		return
	}

	writeJSON(w, http.StatusCreated, img)
}

// DownloadImage handles GET /api/v1/images/{imageID}/download
func (s *Server) DownloadImage(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "imageID")
	img, err := s.storageMgr.GetImage(id)
	if err != nil {
		apiErr := sanitizeManagerError(err)
		writeAPIError(w, statusForAPIError(apiErr, http.StatusNotFound), apiErr)
		return
	}

	w.Header().Set("Content-Disposition", "attachment; filename="+img.Name+".qcow2")
	http.ServeFile(w, r, img.Path)
}
