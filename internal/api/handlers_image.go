package api

import (
	"encoding/json"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/go-chi/chi/v5"
	"github.com/vmsmith/vmsmith/internal/storage"
	"github.com/vmsmith/vmsmith/pkg/types"
)

type createImageRequest struct {
	VMID        string   `json:"vm_id"`
	Name        string   `json:"name"`
	Description string   `json:"description,omitempty"`
	Tags        []string `json:"tags,omitempty"`
}

// CreateImage handles POST /api/v1/images
func (s *Server) CreateImage(w http.ResponseWriter, r *http.Request) {
	var req createImageRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		if isRequestTooLarge(err) {
			writeErrorCode(w, http.StatusRequestEntityTooLarge, "request_too_large", "request body too large")
			return
		}
		writeErrorCode(w, http.StatusBadRequest, "invalid_request_body", "invalid request body")
		return
	}
	if err := validateCreateImageRequest(req.VMID, req.Name); err != nil {
		writeAPIError(w, http.StatusBadRequest, err)
		return
	}
	if err := validateImageDescription(req.Description); err != nil {
		writeAPIError(w, http.StatusBadRequest, err)
		return
	}
	tags, err := normalizeTags(req.Tags)
	if err != nil {
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

	img, err := s.storageMgr.CreateImage(vm.DiskPath, req.Name, vm.ID, storage.CreateImageOptions{
		Description: req.Description,
		Tags:        tags,
	})
	if err != nil {
		apiErr := sanitizeManagerError(err)
		writeAPIError(w, statusForAPIError(apiErr, http.StatusInternalServerError), apiErr)
		return
	}

	s.publishAppEvent("image.created", vm.ID, "image "+img.Name+" created from VM "+vm.Name, map[string]string{
		"image_id":   img.ID,
		"image_name": img.Name,
	})

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

	tagFilter := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("tag")))
	if tagFilter != "" {
		imgs = filterImagesByTag(imgs, tagFilter)
	}

	total := len(imgs)
	pagination := parsePagination(r)
	imgs = paginateSlice(imgs, pagination.Page, pagination.PerPage)
	if imgs == nil {
		imgs = []*types.Image{}
	}
	setTotalCountHeader(w, total)

	writeJSON(w, http.StatusOK, imgs)
}

// UpdateImage handles PATCH /api/v1/images/{imageID}
func (s *Server) UpdateImage(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "imageID")
	var patch types.ImageUpdateSpec
	if err := json.NewDecoder(r.Body).Decode(&patch); err != nil {
		if isRequestTooLarge(err) {
			writeErrorCode(w, http.StatusRequestEntityTooLarge, "request_too_large", "request body too large")
			return
		}
		writeErrorCode(w, http.StatusBadRequest, "invalid_request_body", "invalid request body")
		return
	}
	if err := validateImageDescription(patch.Description); err != nil {
		writeAPIError(w, http.StatusBadRequest, err)
		return
	}
	tags, err := normalizeTags(patch.Tags)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, err)
		return
	}
	if patch.Tags != nil && tags == nil {
		// preserve "explicitly clear" semantics
		tags = []string{}
	}
	patch.Tags = tags

	img, err := s.storageMgr.UpdateImage(id, patch)
	if err != nil {
		apiErr := sanitizeManagerError(err)
		writeAPIError(w, statusForAPIError(apiErr, http.StatusInternalServerError), apiErr)
		return
	}

	s.publishAppEvent("image.updated", "", "image "+img.Name+" metadata updated", map[string]string{
		"image_id":   img.ID,
		"image_name": img.Name,
	})

	writeJSON(w, http.StatusOK, img)
}

// DeleteImage handles DELETE /api/v1/images/{imageID}
func (s *Server) DeleteImage(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "imageID")
	if err := s.storageMgr.DeleteImage(id); err != nil {
		apiErr := sanitizeManagerError(err)
		writeAPIError(w, statusForAPIError(apiErr, http.StatusInternalServerError), apiErr)
		return
	}
	s.publishAppEvent("image.deleted", "", "image "+id+" deleted", map[string]string{
		"image_id": id,
	})
	w.WriteHeader(http.StatusNoContent)
}

var availableStorageBytes = func(path string) (uint64, error) {
	var stat syscall.Statfs_t
	if err := syscall.Statfs(path, &stat); err != nil {
		return 0, err
	}
	return stat.Bavail * uint64(stat.Bsize), nil
}

// UploadImage handles POST /api/v1/images/upload (multipart form: file + name + optional description/tags)
func (s *Server) UploadImage(w http.ResponseWriter, r *http.Request) {
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

	name := strings.TrimSpace(r.FormValue("name"))
	if name == "" {
		// Derive name from filename, strip extension. A filename like ".qcow2"
		// should yield an empty name so the required-name validation below fires.
		filename := strings.TrimSpace(header.Filename)
		if ext := filepath.Ext(filename); ext != "" {
			filename = strings.TrimSpace(strings.TrimSuffix(filename, ext))
		}
		name = filename
	}
	if name == "" {
		writeAPIError(w, http.StatusBadRequest, types.NewAPIError("invalid_image", "image name is required"))
		return
	}

	description := strings.TrimSpace(r.FormValue("description"))
	if err := validateImageDescription(description); err != nil {
		writeAPIError(w, http.StatusBadRequest, err)
		return
	}

	rawTags := parseTagFormValues(r.MultipartForm.Value["tags"])
	tags, err := normalizeTags(rawTags)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, err)
		return
	}

	data, err := io.ReadAll(file)
	if err != nil {
		writeErrorCode(w, http.StatusInternalServerError, "upload_read_failed", "reading upload: "+err.Error())
		return
	}
	if err := validateUploadedImage(header.Filename, data); err != nil {
		writeAPIError(w, http.StatusBadRequest, err)
		return
	}

	freeBytes, err := availableStorageBytes(filepath.Dir(s.storageMgr.ImagePath(name)))
	if err != nil {
		writeErrorCode(w, http.StatusInternalServerError, "storage_check_failed", "checking available storage: "+err.Error())
		return
	}
	if uint64(len(data)) > freeBytes {
		writeAPIError(w, http.StatusInsufficientStorage, types.NewAPIError("insufficient_storage", "not enough free disk space for uploaded image"))
		return
	}

	img, err := s.storageMgr.ImportImage(name, data, storage.CreateImageOptions{
		Description: description,
		Tags:        tags,
	})
	if err != nil {
		apiErr := sanitizeManagerError(err)
		writeAPIError(w, statusForAPIError(apiErr, http.StatusInternalServerError), apiErr)
		return
	}

	s.publishAppEvent("image.uploaded", "", "image "+img.Name+" uploaded", map[string]string{
		"image_id":   img.ID,
		"image_name": img.Name,
	})

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

// filterImagesByTag returns only images whose tag list contains tag (case-insensitive).
func filterImagesByTag(imgs []*types.Image, tag string) []*types.Image {
	out := imgs[:0]
	for _, img := range imgs {
		if img == nil {
			continue
		}
		for _, t := range img.Tags {
			if strings.EqualFold(t, tag) {
				out = append(out, img)
				break
			}
		}
	}
	return out
}

// parseTagFormValues accepts either repeated `tags` form values or a single
// comma-separated value. Whitespace around each entry is trimmed; empty
// entries are dropped before normalization.
func parseTagFormValues(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	var out []string
	for _, v := range values {
		for _, part := range strings.Split(v, ",") {
			if t := strings.TrimSpace(part); t != "" {
				out = append(out, t)
			}
		}
	}
	return out
}
