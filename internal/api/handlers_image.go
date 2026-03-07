package api

import (
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"
)

type createImageRequest struct {
	VMID string `json:"vm_id"`
	Name string `json:"name"`
}

// CreateImage handles POST /api/v1/images
func (s *Server) CreateImage(w http.ResponseWriter, r *http.Request) {
	var req createImageRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	// Get the VM to find its disk path
	vm, err := s.vmManager.Get(r.Context(), req.VMID)
	if err != nil {
		writeError(w, http.StatusNotFound, "VM not found: "+err.Error())
		return
	}

	img, err := s.storageMgr.CreateImage(vm.DiskPath, req.Name, vm.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusCreated, img)
}

// ListImages handles GET /api/v1/images
func (s *Server) ListImages(w http.ResponseWriter, r *http.Request) {
	imgs, err := s.storageMgr.ListImages()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, imgs)
}

// DeleteImage handles DELETE /api/v1/images/{imageID}
func (s *Server) DeleteImage(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "imageID")
	if err := s.storageMgr.DeleteImage(id); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// DownloadImage handles GET /api/v1/images/{imageID}/download
func (s *Server) DownloadImage(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "imageID")
	img, err := s.storageMgr.GetImage(id)
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}

	w.Header().Set("Content-Disposition", "attachment; filename="+img.Name+".qcow2")
	http.ServeFile(w, r, img.Path)
}
