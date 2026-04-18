package api

import (
	"net/http"
	"testing"

	"github.com/vmsmith/vmsmith/pkg/types"
)

func TestTemplateCRUD(t *testing.T) {
	ts, _, cleanup := testServer(t)
	defer cleanup()

	resp, err := http.Post(ts.URL+"/api/v1/templates", "application/json", jsonBody(t, map[string]any{
		"name":         "small-linux",
		"image":        "ubuntu-22.04",
		"cpus":         2,
		"ram_mb":       2048,
		"disk_gb":      20,
		"description":  "  small template  ",
		"default_user": " ubuntu ",
		"tags":         []string{"Prod", "web", "prod"},
	}))
	if err != nil {
		t.Fatalf("POST /templates: %v", err)
	}
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create status = %d, want 201", resp.StatusCode)
	}

	var created types.VMTemplate
	decodeJSON(t, resp, &created)
	if created.ID == "" {
		t.Fatal("expected template ID")
	}
	if created.Name != "small-linux" {
		t.Fatalf("Name = %q, want small-linux", created.Name)
	}
	if created.Description != "small template" {
		t.Fatalf("Description = %q, want trimmed description", created.Description)
	}
	if created.DefaultUser != "ubuntu" {
		t.Fatalf("DefaultUser = %q, want ubuntu", created.DefaultUser)
	}
	if len(created.Tags) != 2 || created.Tags[0] != "prod" || created.Tags[1] != "web" {
		t.Fatalf("Tags = %#v, want [prod web]", created.Tags)
	}

	listResp, err := http.Get(ts.URL + "/api/v1/templates")
	if err != nil {
		t.Fatalf("GET /templates: %v", err)
	}
	if listResp.StatusCode != http.StatusOK {
		t.Fatalf("list status = %d, want 200", listResp.StatusCode)
	}
	if got := listResp.Header.Get("X-Total-Count"); got != "1" {
		t.Fatalf("X-Total-Count = %q, want 1", got)
	}
	var templates []types.VMTemplate
	decodeJSON(t, listResp, &templates)
	if len(templates) != 1 || templates[0].ID != created.ID {
		t.Fatalf("templates = %#v, want created template", templates)
	}

	req, err := http.NewRequest(http.MethodDelete, ts.URL+"/api/v1/templates/"+created.ID, nil)
	if err != nil {
		t.Fatalf("DELETE request: %v", err)
	}
	deleteResp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("DELETE /templates/{id}: %v", err)
	}
	if deleteResp.StatusCode != http.StatusNoContent {
		t.Fatalf("delete status = %d, want 204", deleteResp.StatusCode)
	}

	listResp, err = http.Get(ts.URL + "/api/v1/templates")
	if err != nil {
		t.Fatalf("GET /templates after delete: %v", err)
	}
	var empty []types.VMTemplate
	decodeJSON(t, listResp, &empty)
	if len(empty) != 0 {
		t.Fatalf("expected no templates after delete, got %#v", empty)
	}
}

func TestCreateTemplateValidation(t *testing.T) {
	ts, _, cleanup := testServer(t)
	defer cleanup()

	resp, err := http.Post(ts.URL+"/api/v1/templates", "application/json", jsonBody(t, map[string]any{
		"name":   "bad name!",
		"image":  "ubuntu-22.04",
		"ram_mb": 64,
	}))
	if err != nil {
		t.Fatalf("POST /templates invalid: %v", err)
	}
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
	assertAPIErrorCode(t, resp, "invalid_name")
}

func TestCreateTemplateRejectsDuplicateName(t *testing.T) {
	ts, _, cleanup := testServer(t)
	defer cleanup()

	for i, name := range []string{"base-template", " Base-Template "} {
		resp, err := http.Post(ts.URL+"/api/v1/templates", "application/json", jsonBody(t, map[string]any{
			"name":  name,
			"image": "ubuntu-22.04",
		}))
		if err != nil {
			t.Fatalf("POST /templates #%d: %v", i, err)
		}
		if i == 0 {
			if resp.StatusCode != http.StatusCreated {
				t.Fatalf("first create status = %d, want 201", resp.StatusCode)
			}
			resp.Body.Close()
			continue
		}
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("duplicate status = %d, want 400", resp.StatusCode)
		}
		assertAPIErrorCode(t, resp, "invalid_name")
	}
}

func TestDeleteTemplateNotFound(t *testing.T) {
	ts, _, cleanup := testServer(t)
	defer cleanup()

	req, err := http.NewRequest(http.MethodDelete, ts.URL+"/api/v1/templates/missing", nil)
	if err != nil {
		t.Fatalf("DELETE request: %v", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("DELETE /templates/missing: %v", err)
	}
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", resp.StatusCode)
	}
	assertAPIErrorCode(t, resp, "resource_not_found")
}
