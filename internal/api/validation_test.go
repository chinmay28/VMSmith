package api

import (
	"errors"
	"strings"
	"testing"

	"github.com/vmsmith/vmsmith/pkg/types"
)

func TestValidateVMSpec(t *testing.T) {
	tests := []struct {
		name        string
		spec        types.VMSpec
		wantCode    string
		wantMessage string
	}{
		{
			name: "valid minimal spec",
			spec: types.VMSpec{Name: "web-01", Image: "ubuntu-22.04", CPUs: 2, RAMMB: 2048, DiskGB: 20},
		},
		{
			name: "create may omit resource values to use defaults",
			spec: types.VMSpec{Name: "valid-name", Image: "ubuntu", CPUs: 0, RAMMB: 0, DiskGB: 0},
		},
		{
			name:        "missing name after trim",
			spec:        types.VMSpec{Name: "   ", Image: "ubuntu"},
			wantCode:    "invalid_name",
			wantMessage: "vm name is required",
		},
		{
			name:        "name with spaces and punctuation",
			spec:        types.VMSpec{Name: "bad name!", Image: "ubuntu"},
			wantCode:    "invalid_name",
			wantMessage: "vm name must be 1-64 characters and contain only letters, numbers, and hyphens",
		},
		{
			name:        "name cannot start with hyphen",
			spec:        types.VMSpec{Name: "-badname", Image: "ubuntu"},
			wantCode:    "invalid_name",
			wantMessage: "vm name must be 1-64 characters and contain only letters, numbers, and hyphens",
		},
		{
			name:        "name cannot end with hyphen",
			spec:        types.VMSpec{Name: "badname-", Image: "ubuntu"},
			wantCode:    "invalid_name",
			wantMessage: "vm name must be 1-64 characters and contain only letters, numbers, and hyphens",
		},
		{
			name:        "name over max length",
			spec:        types.VMSpec{Name: strings.Repeat("a", 65), Image: "ubuntu"},
			wantCode:    "invalid_name",
			wantMessage: "vm name must be 1-64 characters and contain only letters, numbers, and hyphens",
		},
		{
			name:        "missing image after trim",
			spec:        types.VMSpec{Name: "valid-name", Image: "   "},
			wantCode:    "invalid_image",
			wantMessage: "image is required",
		},
		{
			name:        "cpu below minimum",
			spec:        types.VMSpec{Name: "valid-name", Image: "ubuntu", CPUs: -1},
			wantCode:    "invalid_spec",
			wantMessage: "cpus must be between 1 and 128",
		},
		{
			name:        "cpu above maximum",
			spec:        types.VMSpec{Name: "valid-name", Image: "ubuntu", CPUs: 129},
			wantCode:    "invalid_spec",
			wantMessage: "cpus must be between 1 and 128",
		},
		{
			name:        "ram below minimum",
			spec:        types.VMSpec{Name: "valid-name", Image: "ubuntu", RAMMB: 127},
			wantCode:    "invalid_spec",
			wantMessage: "ram_mb must be between 128 and 1048576",
		},
		{
			name:        "ram above maximum",
			spec:        types.VMSpec{Name: "valid-name", Image: "ubuntu", RAMMB: 1024*1024 + 1},
			wantCode:    "invalid_spec",
			wantMessage: "ram_mb must be between 128 and 1048576",
		},
		{
			name:        "disk explicit invalid negative",
			spec:        types.VMSpec{Name: "valid-name", Image: "ubuntu", DiskGB: -1},
			wantCode:    "invalid_spec",
			wantMessage: "disk_gb must be between 1 and 10240",
		},
		{
			name:        "disk above maximum",
			spec:        types.VMSpec{Name: "valid-name", Image: "ubuntu", DiskGB: 10241},
			wantCode:    "invalid_spec",
			wantMessage: "disk_gb must be between 1 and 10240",
		},
		{
			name:        "invalid nat cidr",
			spec:        types.VMSpec{Name: "valid-name", Image: "ubuntu", NatStaticIP: "not-a-cidr"},
			wantCode:    "invalid_spec",
			wantMessage: "nat_static_ip must be valid CIDR notation, e.g. 192.168.100.50/24",
		},
		{
			name:        "invalid nat gateway",
			spec:        types.VMSpec{Name: "valid-name", Image: "ubuntu", NatGateway: "not-an-ip"},
			wantCode:    "invalid_spec",
			wantMessage: "nat_gateway must be a valid IP address",
		},
		{
			name: "valid nat settings",
			spec: types.VMSpec{Name: "valid-name", Image: "ubuntu", NatStaticIP: "192.168.100.50/24", NatGateway: "192.168.100.1"},
		},
		{
			name: "valid tags",
			spec: types.VMSpec{Name: "valid-name", Image: "ubuntu", Tags: []string{"prod", "web-1"}},
		},
		{
			name:        "invalid empty tag",
			spec:        types.VMSpec{Name: "valid-name", Image: "ubuntu", Tags: []string{"prod", "  "}},
			wantCode:    "invalid_spec",
			wantMessage: "tags cannot contain empty values",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateVMSpec(tt.spec)
			assertAPIError(t, err, tt.wantCode, tt.wantMessage)
		})
	}
}

func TestValidateVMUpdateSpec(t *testing.T) {
	tests := []struct {
		name        string
		patch       types.VMUpdateSpec
		wantCode    string
		wantMessage string
	}{
		{
			name:  "empty patch allowed",
			patch: types.VMUpdateSpec{},
		},
		{
			name:        "cpu below minimum",
			patch:       types.VMUpdateSpec{CPUs: -1},
			wantCode:    "invalid_spec",
			wantMessage: "cpus must be between 1 and 128",
		},
		{
			name:        "cpu above maximum",
			patch:       types.VMUpdateSpec{CPUs: 129},
			wantCode:    "invalid_spec",
			wantMessage: "cpus must be between 1 and 128",
		},
		{
			name:        "ram below minimum",
			patch:       types.VMUpdateSpec{RAMMB: 127},
			wantCode:    "invalid_spec",
			wantMessage: "ram_mb must be between 128 and 1048576",
		},
		{
			name:        "ram above maximum",
			patch:       types.VMUpdateSpec{RAMMB: 1024*1024 + 1},
			wantCode:    "invalid_spec",
			wantMessage: "ram_mb must be between 128 and 1048576",
		},
		{
			name:        "disk below minimum",
			patch:       types.VMUpdateSpec{DiskGB: -1},
			wantCode:    "invalid_spec",
			wantMessage: "disk_gb must be between 1 and 10240",
		},
		{
			name:        "disk above maximum",
			patch:       types.VMUpdateSpec{DiskGB: 10241},
			wantCode:    "invalid_spec",
			wantMessage: "disk_gb must be between 1 and 10240",
		},
		{
			name:        "invalid nat cidr",
			patch:       types.VMUpdateSpec{NatStaticIP: "not-a-cidr"},
			wantCode:    "invalid_spec",
			wantMessage: "nat_static_ip must be valid CIDR notation, e.g. 192.168.100.50/24",
		},
		{
			name:        "invalid nat gateway",
			patch:       types.VMUpdateSpec{NatGateway: "not-an-ip"},
			wantCode:    "invalid_spec",
			wantMessage: "nat_gateway must be a valid IP address",
		},
		{
			name:  "valid nat settings",
			patch: types.VMUpdateSpec{NatStaticIP: "192.168.100.60/24", NatGateway: "192.168.100.1"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateVMUpdateSpec(tt.patch)
			assertAPIError(t, err, tt.wantCode, tt.wantMessage)
		})
	}
}

func TestValidateUniqueVMName(t *testing.T) {
	err := validateUniqueVMName("web-01", []*types.VM{{Name: "db-01"}, {Name: " WEB-01 "}})
	assertAPIError(t, err, "invalid_name", "vm name \"web-01\" already exists")

	err = validateUniqueVMName("worker-01", []*types.VM{{Name: "db-01"}, nil})
	assertAPIError(t, err, "", "")
}

func TestValidatePortForward(t *testing.T) {
	tests := []struct {
		name        string
		hostPort    int
		guestPort   int
		proto       types.Protocol
		wantCode    string
		wantMessage string
	}{
		{
			name:      "valid tcp",
			hostPort:  2222,
			guestPort: 22,
			proto:     types.ProtocolTCP,
		},
		{
			name:        "host port below minimum",
			hostPort:    0,
			guestPort:   22,
			proto:       types.ProtocolTCP,
			wantCode:    "invalid_port_forward",
			wantMessage: "host_port must be between 1 and 65535",
		},
		{
			name:        "guest port above maximum",
			hostPort:    2222,
			guestPort:   65536,
			proto:       types.ProtocolTCP,
			wantCode:    "invalid_port_forward",
			wantMessage: "guest_port must be between 1 and 65535",
		},
		{
			name:        "invalid protocol",
			hostPort:    2222,
			guestPort:   22,
			proto:       types.Protocol("icmp"),
			wantCode:    "invalid_port_forward",
			wantMessage: "protocol must be tcp or udp",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validatePortForward(tt.hostPort, tt.guestPort, tt.proto)
			assertAPIError(t, err, tt.wantCode, tt.wantMessage)
		})
	}
}

func TestValidateUploadedImage(t *testing.T) {
	tests := []struct {
		name        string
		filename    string
		data        []byte
		wantCode    string
		wantMessage string
	}{
		{name: "valid qcow2 upload", filename: "ubuntu.qcow2", data: []byte("qcow2-data")},
		{name: "filename required", filename: "   ", data: []byte("qcow2-data"), wantCode: "invalid_image", wantMessage: "uploaded filename is required"},
		{name: "extension must be qcow2", filename: "ubuntu.iso", data: []byte("iso-data"), wantCode: "invalid_image", wantMessage: "uploaded file must have a .qcow2 extension"},
		{name: "extension check is case insensitive", filename: "ubuntu.QCOW2", data: []byte("qcow2-data")},
		{name: "empty upload rejected", filename: "empty.qcow2", data: nil, wantCode: "invalid_image", wantMessage: "uploaded image file cannot be empty"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateUploadedImage(tt.filename, tt.data)
			assertAPIError(t, err, tt.wantCode, tt.wantMessage)
		})
	}
}

func TestSanitizeManagerError(t *testing.T) {
	tests := []struct {
		name        string
		err         error
		wantCode    string
		wantMessage string
	}{
		{name: "nil error", err: nil},
		{name: "existing api error", err: types.NewAPIError("invalid_name", "vm name is required"), wantCode: "invalid_name", wantMessage: "vm name is required"},
		{name: "resource not found", err: errors.New("vm not found"), wantCode: "resource_not_found", wantMessage: "resource not found"},
		{name: "disk shrink rejected", err: errors.New("disk can only grow"), wantCode: "disk_shrink_not_allowed", wantMessage: "disk can only grow"},
		{name: "invalid nat ip", err: errors.New("invalid nat_static_ip in update"), wantCode: "invalid_spec", wantMessage: "nat_static_ip must be valid CIDR notation, e.g. 192.168.100.50/24"},
		{name: "libvirt connection error is sanitized", err: errors.New("connecting to libvirt (qemu:///system): authentication failed: details"), wantCode: "service_unavailable", wantMessage: "vm backend is unavailable"},
		{name: "network setup error is sanitized", err: errors.New("ensuring NAT network: defining network: libvirt secret details"), wantCode: "network_unavailable", wantMessage: "vm network is unavailable"},
		{name: "disk tooling error is sanitized", err: errors.New("resizing disk: qemu-img resize failed with backend details"), wantCode: "storage_error", wantMessage: "vm disk operation failed"},
		{name: "cloud-init tooling error is sanitized", err: errors.New("creating cloud-init ISO: genisoimage: missing binary on host"), wantCode: "config_generation_failed", wantMessage: "vm configuration generation failed"},
		{name: "domain definition error is sanitized", err: errors.New("defining domain: libvirt error: XML error details"), wantCode: "vm_definition_failed", wantMessage: "vm definition failed"},
		{name: "vm state change error is sanitized", err: errors.New("starting domain: libvirt error: permission denied"), wantCode: "vm_state_change_failed", wantMessage: "vm state change failed"},
		{name: "snapshot error is sanitized", err: errors.New("creating snapshot: libvirt error: snapshot backend details"), wantCode: "snapshot_operation_failed", wantMessage: "snapshot operation failed"},
		{name: "generic internal error", err: errors.New("libvirt exploded with details"), wantCode: "internal_error", wantMessage: "operation failed"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := sanitizeManagerError(tt.err)
			assertAPIError(t, err, tt.wantCode, tt.wantMessage)
		})
	}
}

func assertAPIError(t *testing.T, err error, wantCode, wantMessage string) {
	t.Helper()

	if wantCode == "" {
		if err != nil {
			t.Fatalf("expected nil error, got %v", err)
		}
		return
	}

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
	if apiErr.Message != wantMessage {
		t.Fatalf("error message = %q, want %q", apiErr.Message, wantMessage)
	}
}
