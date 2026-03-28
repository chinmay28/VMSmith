package api

import (
	"errors"
	"strings"
	"testing"

	"github.com/vmsmith/vmsmith/pkg/types"
)

func assertAPIError(t *testing.T, err error, wantCode string) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected API error %q, got nil", wantCode)
	}
	var apiErr *types.APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("expected APIError, got %T: %v", err, err)
	}
	if apiErr.Code != wantCode {
		t.Fatalf("error code = %q, want %q", apiErr.Code, wantCode)
	}
	if strings.TrimSpace(apiErr.Message) == "" {
		t.Fatal("expected non-empty error message")
	}
}

func TestValidateVMSpec(t *testing.T) {
	tests := []struct {
		name     string
		spec     types.VMSpec
		wantCode string
	}{
		{
			name: "valid minimal spec",
			spec: types.VMSpec{Name: "web-01", Image: "ubuntu-22.04", CPUs: 2, RAMMB: 2048, DiskGB: 20},
		},
		{
			name:     "missing name",
			spec:     types.VMSpec{Image: "ubuntu-22.04", CPUs: 2, RAMMB: 2048},
			wantCode: "invalid_name",
		},
		{
			name:     "blank name after trim",
			spec:     types.VMSpec{Name: "   ", Image: "ubuntu-22.04", CPUs: 2, RAMMB: 2048},
			wantCode: "invalid_name",
		},
		{
			name:     "name with spaces",
			spec:     types.VMSpec{Name: "bad name", Image: "ubuntu-22.04", CPUs: 2, RAMMB: 2048},
			wantCode: "invalid_name",
		},
		{
			name:     "name too long",
			spec:     types.VMSpec{Name: strings.Repeat("a", 65), Image: "ubuntu-22.04", CPUs: 2, RAMMB: 2048},
			wantCode: "invalid_name",
		},
		{
			name:     "missing image",
			spec:     types.VMSpec{Name: "web-01", CPUs: 2, RAMMB: 2048},
			wantCode: "invalid_image",
		},
		{
			name:     "cpus below minimum",
			spec:     types.VMSpec{Name: "web-01", Image: "ubuntu-22.04", CPUs: -1, RAMMB: 2048},
			wantCode: "invalid_spec",
		},
		{
			name:     "cpus above maximum",
			spec:     types.VMSpec{Name: "web-01", Image: "ubuntu-22.04", CPUs: 129, RAMMB: 2048},
			wantCode: "invalid_spec",
		},
		{
			name:     "ram below minimum",
			spec:     types.VMSpec{Name: "web-01", Image: "ubuntu-22.04", CPUs: 2, RAMMB: 127},
			wantCode: "invalid_spec",
		},
		{
			name:     "ram above maximum",
			spec:     types.VMSpec{Name: "web-01", Image: "ubuntu-22.04", CPUs: 2, RAMMB: 1024*1024 + 1},
			wantCode: "invalid_spec",
		},
		{
			name: "disk omitted is allowed",
			spec: types.VMSpec{Name: "web-01", Image: "ubuntu-22.04", CPUs: 2, RAMMB: 2048, DiskGB: 0},
		},
		{
			name:     "disk invalid negative",
			spec:     types.VMSpec{Name: "web-01", Image: "ubuntu-22.04", CPUs: 2, RAMMB: 2048, DiskGB: -1},
			wantCode: "invalid_spec",
		},
		{
			name:     "disk above maximum",
			spec:     types.VMSpec{Name: "web-01", Image: "ubuntu-22.04", CPUs: 2, RAMMB: 2048, DiskGB: 10241},
			wantCode: "invalid_spec",
		},
		{
			name:     "invalid nat static ip",
			spec:     types.VMSpec{Name: "web-01", Image: "ubuntu-22.04", CPUs: 2, RAMMB: 2048, NatStaticIP: "192.168.1.10"},
			wantCode: "invalid_spec",
		},
		{
			name:     "invalid nat gateway",
			spec:     types.VMSpec{Name: "web-01", Image: "ubuntu-22.04", CPUs: 2, RAMMB: 2048, NatGateway: "not-an-ip"},
			wantCode: "invalid_spec",
		},
		{
			name: "valid nat fields",
			spec: types.VMSpec{Name: "web-01", Image: "ubuntu-22.04", CPUs: 2, RAMMB: 2048, NatStaticIP: "192.168.1.10/24", NatGateway: "192.168.1.1"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateVMSpec(tt.spec)
			if tt.wantCode == "" {
				if err != nil {
					t.Fatalf("validateVMSpec() error = %v, want nil", err)
				}
				return
			}
			assertAPIError(t, err, tt.wantCode)
		})
	}
}

func TestValidateVMUpdateSpec(t *testing.T) {
	tests := []struct {
		name     string
		patch    types.VMUpdateSpec
		wantCode string
	}{
		{
			name:  "valid empty patch",
			patch: types.VMUpdateSpec{},
		},
		{
			name:     "cpus below minimum",
			patch:    types.VMUpdateSpec{CPUs: -1},
			wantCode: "invalid_spec",
		},
		{
			name:     "cpus above maximum",
			patch:    types.VMUpdateSpec{CPUs: 129},
			wantCode: "invalid_spec",
		},
		{
			name:     "ram below minimum",
			patch:    types.VMUpdateSpec{RAMMB: 127},
			wantCode: "invalid_spec",
		},
		{
			name:     "ram above maximum",
			patch:    types.VMUpdateSpec{RAMMB: 1024*1024 + 1},
			wantCode: "invalid_spec",
		},
		{
			name:     "disk below minimum",
			patch:    types.VMUpdateSpec{DiskGB: -1},
			wantCode: "invalid_spec",
		},
		{
			name:     "disk above maximum",
			patch:    types.VMUpdateSpec{DiskGB: 10241},
			wantCode: "invalid_spec",
		},
		{
			name:     "invalid nat static ip",
			patch:    types.VMUpdateSpec{NatStaticIP: "10.0.0.5"},
			wantCode: "invalid_spec",
		},
		{
			name:     "invalid nat gateway",
			patch:    types.VMUpdateSpec{NatGateway: "bad-ip"},
			wantCode: "invalid_spec",
		},
		{
			name:  "valid nat fields",
			patch: types.VMUpdateSpec{NatStaticIP: "10.0.0.5/24", NatGateway: "10.0.0.1"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateVMUpdateSpec(tt.patch)
			if tt.wantCode == "" {
				if err != nil {
					t.Fatalf("validateVMUpdateSpec() error = %v, want nil", err)
				}
				return
			}
			assertAPIError(t, err, tt.wantCode)
		})
	}
}
