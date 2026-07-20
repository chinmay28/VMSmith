package storage

import (
	"archive/tar"
	"crypto/sha256"
	"encoding/hex"
	"encoding/xml"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/vmsmith/vmsmith/pkg/types"
)

// OVF (Open Virtualization Format) support — roadmap 5.3.
//
// Export packages a stopped VM as a single-file OVA: a tar archive whose
// first entry is the OVF descriptor, followed by a streamOptimized VMDK
// (converted from the VM's qcow2 disk via qemu-img, which flattens any
// backing chain) and a SHA256 manifest. Import reverses the process:
// extract, parse the descriptor for CPU/RAM/disk sizing, convert the disk
// back to qcow2, and register it as a VMSmith image so a VM can be created
// from it.

// CIM resource types used in OVF VirtualHardwareSection items.
const (
	ovfResourceCPU     = 3
	ovfResourceMemory  = 4
	ovfResourceNIC     = 10
	ovfResourceSCSICtl = 6
	ovfResourceDisk    = 17
)

// ovaMaxExtractBytes caps the total bytes extracted from an OVA so a
// maliciously-crafted archive cannot exhaust the disk (decompression-bomb
// guard; the HTTP upload limit bounds the archive itself, this bounds the
// expansion).
const ovaMaxExtractBytes = 512 << 30 // 512 GiB

// --- OVF descriptor model (shared by generation and parsing) ---

type ovfEnvelope struct {
	XMLName        xml.Name          `xml:"Envelope"`
	Xmlns          string            `xml:"xmlns,attr,omitempty"`
	XmlnsOVF       string            `xml:"xmlns:ovf,attr,omitempty"`
	XmlnsRasd      string            `xml:"xmlns:rasd,attr,omitempty"`
	XmlnsVssd      string            `xml:"xmlns:vssd,attr,omitempty"`
	References     ovfReferences     `xml:"References"`
	DiskSection    ovfDiskSection    `xml:"DiskSection"`
	NetworkSection ovfNetworkSection `xml:"NetworkSection"`
	VirtualSystem  ovfVirtualSystem  `xml:"VirtualSystem"`
}

type ovfReferences struct {
	Files []ovfFile `xml:"File"`
}

type ovfFile struct {
	Href string `xml:"href,attr"`
	ID   string `xml:"id,attr"`
	Size int64  `xml:"size,attr,omitempty"`
}

type ovfDiskSection struct {
	Info  string    `xml:"Info"`
	Disks []ovfDisk `xml:"Disk"`
}

type ovfDisk struct {
	Capacity                string `xml:"capacity,attr"`
	CapacityAllocationUnits string `xml:"capacityAllocationUnits,attr,omitempty"`
	DiskID                  string `xml:"diskId,attr"`
	FileRef                 string `xml:"fileRef,attr"`
	Format                  string `xml:"format,attr,omitempty"`
}

type ovfNetworkSection struct {
	Info     string       `xml:"Info"`
	Networks []ovfNetwork `xml:"Network"`
}

type ovfNetwork struct {
	Name        string `xml:"name,attr"`
	Description string `xml:"Description,omitempty"`
}

type ovfVirtualSystem struct {
	ID              string             `xml:"id,attr"`
	Info            string             `xml:"Info"`
	Name            string             `xml:"Name,omitempty"`
	HardwareSection ovfHardwareSection `xml:"VirtualHardwareSection"`
}

type ovfHardwareSection struct {
	Info  string    `xml:"Info"`
	Items []ovfItem `xml:"Item"`
}

type ovfItem struct {
	Description     string `xml:"Description,omitempty"`
	ElementName     string `xml:"ElementName,omitempty"`
	InstanceID      string `xml:"InstanceID,omitempty"`
	ResourceType    int    `xml:"ResourceType"`
	VirtualQuantity int64  `xml:"VirtualQuantity,omitempty"`
	AllocationUnits string `xml:"AllocationUnits,omitempty"`
	HostResource    string `xml:"HostResource,omitempty"`
	Parent          string `xml:"Parent,omitempty"`
	Connection      string `xml:"Connection,omitempty"`
}

// buildOVFDescriptor renders the OVF XML for a VM being exported.
func buildOVFDescriptor(vm *types.VM, diskFileName string, diskFileSize int64) ([]byte, error) {
	capacityGB := int64(vm.Spec.DiskGB)
	if capacityGB <= 0 {
		capacityGB = 1
	}

	env := ovfEnvelope{
		Xmlns:    "http://schemas.dmtf.org/ovf/envelope/1",
		XmlnsOVF: "http://schemas.dmtf.org/ovf/envelope/1",
		XmlnsRasd: "http://schemas.dmtf.org/wbem/wscim/1/cim-schema/2/" +
			"CIM_ResourceAllocationSettingData",
		XmlnsVssd: "http://schemas.dmtf.org/wbem/wscim/1/cim-schema/2/" +
			"CIM_VirtualSystemSettingData",
		References: ovfReferences{
			Files: []ovfFile{{Href: diskFileName, ID: "file1", Size: diskFileSize}},
		},
		DiskSection: ovfDiskSection{
			Info: "Virtual disk information",
			Disks: []ovfDisk{{
				Capacity:                strconv.FormatInt(capacityGB, 10),
				CapacityAllocationUnits: "byte * 2^30",
				DiskID:                  "vmdisk1",
				FileRef:                 "file1",
				Format:                  "http://www.vmware.com/interfaces/specifications/vmdk.html#streamOptimized",
			}},
		},
		NetworkSection: ovfNetworkSection{
			Info:     "The list of logical networks",
			Networks: []ovfNetwork{{Name: "nat", Description: "VMSmith NAT network"}},
		},
		VirtualSystem: ovfVirtualSystem{
			ID:   vm.Name,
			Info: "Exported by VMSmith",
			Name: vm.Name,
			HardwareSection: ovfHardwareSection{
				Info: "Virtual hardware requirements",
				Items: []ovfItem{
					{
						Description:     "Number of Virtual CPUs",
						ElementName:     fmt.Sprintf("%d virtual CPU(s)", vm.Spec.CPUs),
						InstanceID:      "1",
						ResourceType:    ovfResourceCPU,
						VirtualQuantity: int64(vm.Spec.CPUs),
						AllocationUnits: "hertz * 10^6",
					},
					{
						Description:     "Memory Size",
						ElementName:     fmt.Sprintf("%dMB of memory", vm.Spec.RAMMB),
						InstanceID:      "2",
						ResourceType:    ovfResourceMemory,
						VirtualQuantity: int64(vm.Spec.RAMMB),
						AllocationUnits: "byte * 2^20",
					},
					{
						Description:  "SCSI Controller",
						ElementName:  "scsiController0",
						InstanceID:   "3",
						ResourceType: ovfResourceSCSICtl,
					},
					{
						Description:  "Disk drive",
						ElementName:  "disk1",
						InstanceID:   "4",
						ResourceType: ovfResourceDisk,
						HostResource: "ovf:/disk/vmdisk1",
						Parent:       "3",
					},
					{
						Description:  "Network adapter",
						ElementName:  "ethernet0",
						InstanceID:   "5",
						ResourceType: ovfResourceNIC,
						Connection:   "nat",
					},
				},
			},
		},
	}

	body, err := xml.MarshalIndent(env, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("marshalling OVF descriptor: %w", err)
	}
	return append([]byte(xml.Header), append(body, '\n')...), nil
}

// ExportOVA packages the given (stopped) VM as an OVA at outputPath.
// The VM's qcow2 disk is flattened + converted to a streamOptimized VMDK
// via qemu-img; the descriptor and a SHA256 manifest ride alongside it in
// the tar. Progress, when non-nil, receives the qemu-img conversion
// percentage (0-100).
func (m *Manager) ExportOVA(vm *types.VM, outputPath string, progress func(float64)) error {
	if vm.DiskPath == "" {
		return fmt.Errorf("vm %s has no disk path", vm.ID)
	}
	if _, err := os.Stat(vm.DiskPath); err != nil {
		return fmt.Errorf("vm disk not accessible: %w", err)
	}

	workDir, err := os.MkdirTemp(filepath.Dir(outputPath), ".ova-export-*")
	if err != nil {
		return fmt.Errorf("creating export workdir: %w", err)
	}
	defer os.RemoveAll(workDir)

	diskFileName := vm.Name + "-disk1.vmdk"
	vmdkPath := filepath.Join(workDir, diskFileName)

	args := []string{"convert", "-f", "qcow2", "-O", "vmdk", "-o", "subformat=streamOptimized"}
	if progress != nil {
		args = append(args, "-p")
	}
	args = append(args, vm.DiskPath, vmdkPath)
	cmd := exec.Command("qemu-img", args...)
	if progress == nil {
		if out, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("qemu-img convert to vmdk: %s: %w", string(out), err)
		}
	} else if err := runConvertWithProgress(cmd, progress); err != nil {
		return err
	}

	vmdkInfo, err := os.Stat(vmdkPath)
	if err != nil {
		return fmt.Errorf("stat converted vmdk: %w", err)
	}

	ovfName := vm.Name + ".ovf"
	descriptor, err := buildOVFDescriptor(vm, diskFileName, vmdkInfo.Size())
	if err != nil {
		return err
	}

	manifest := fmt.Sprintf("SHA256(%s)= %s\n", ovfName, sha256Hex(descriptor))
	vmdkSum, err := sha256File(vmdkPath)
	if err != nil {
		return err
	}
	manifest += fmt.Sprintf("SHA256(%s)= %s\n", diskFileName, vmdkSum)

	// Assemble the tar. Per the OVF spec the descriptor must be the first
	// entry in the archive.
	tmpOVA := filepath.Join(workDir, "bundle.ova")
	if err := writeOVATar(tmpOVA, ovfName, descriptor, vmdkPath, diskFileName, vm.Name+".mf", []byte(manifest)); err != nil {
		return err
	}
	if err := os.Rename(tmpOVA, outputPath); err != nil {
		return fmt.Errorf("moving OVA into place: %w", err)
	}
	return nil
}

func writeOVATar(dest, ovfName string, descriptor []byte, vmdkPath, vmdkName, mfName string, manifest []byte) error {
	out, err := os.Create(dest)
	if err != nil {
		return fmt.Errorf("creating OVA: %w", err)
	}
	defer out.Close()

	tw := tar.NewWriter(out)
	now := time.Now()

	writeBytes := func(name string, data []byte) error {
		if err := tw.WriteHeader(&tar.Header{
			Name: name, Mode: 0o644, Size: int64(len(data)), ModTime: now,
		}); err != nil {
			return err
		}
		_, err := tw.Write(data)
		return err
	}

	if err := writeBytes(ovfName, descriptor); err != nil {
		return fmt.Errorf("writing OVF entry: %w", err)
	}

	vmdkFile, err := os.Open(vmdkPath)
	if err != nil {
		return fmt.Errorf("opening vmdk: %w", err)
	}
	defer vmdkFile.Close()
	vmdkInfo, err := vmdkFile.Stat()
	if err != nil {
		return err
	}
	if err := tw.WriteHeader(&tar.Header{
		Name: vmdkName, Mode: 0o644, Size: vmdkInfo.Size(), ModTime: now,
	}); err != nil {
		return err
	}
	if _, err := io.Copy(tw, vmdkFile); err != nil {
		return fmt.Errorf("writing vmdk entry: %w", err)
	}

	if err := writeBytes(mfName, manifest); err != nil {
		return fmt.Errorf("writing manifest entry: %w", err)
	}
	if err := tw.Close(); err != nil {
		return fmt.Errorf("finalizing OVA tar: %w", err)
	}
	return nil
}

// OVAImportResult carries everything the caller needs to create a VM that
// matches the imported appliance.
type OVAImportResult struct {
	Image *types.Image
	// Name is the VirtualSystem id/name from the descriptor ("" when absent).
	Name string
	// CPUs / RAMMB / DiskGB parsed from the descriptor (0 when absent).
	CPUs   int
	RAMMB  int
	DiskGB int
}

// ImportOVA extracts an OVA (or bare OVF alongside its disk) and registers
// the converted qcow2 as a VMSmith image named imageName.
func (m *Manager) ImportOVA(ovaPath, imageName string) (*OVAImportResult, error) {
	workDir, err := os.MkdirTemp(m.cfg.Storage.ImagesDir, ".ova-import-*")
	if err != nil {
		return nil, fmt.Errorf("creating import workdir: %w", err)
	}
	defer os.RemoveAll(workDir)

	var ovfPath string
	if strings.HasSuffix(strings.ToLower(ovaPath), ".ovf") {
		// Bare descriptor: disks are referenced relative to it.
		ovfPath = ovaPath
	} else {
		ovfPath, err = extractOVA(ovaPath, workDir)
		if err != nil {
			return nil, err
		}
	}

	descriptor, err := os.ReadFile(ovfPath)
	if err != nil {
		return nil, fmt.Errorf("reading OVF descriptor: %w", err)
	}
	parsed, diskHref, err := parseOVFDescriptor(descriptor)
	if err != nil {
		return nil, err
	}
	if diskHref == "" {
		return nil, fmt.Errorf("OVF descriptor references no disk file")
	}

	// The href is relative to the descriptor. Reject traversal.
	if filepath.IsAbs(diskHref) || strings.Contains(diskHref, "..") {
		return nil, fmt.Errorf("OVF disk reference %q is not a safe relative path", diskHref)
	}
	diskPath := filepath.Join(filepath.Dir(ovfPath), diskHref)
	if _, err := os.Stat(diskPath); err != nil {
		return nil, fmt.Errorf("OVF-referenced disk %q not found: %w", diskHref, err)
	}

	if err := os.MkdirAll(m.cfg.Storage.ImagesDir, 0o755); err != nil {
		return nil, err
	}
	imagePath := m.ImagePath(imageName)
	if _, err := os.Stat(imagePath); err == nil {
		return nil, fmt.Errorf("image %q already exists", imageName)
	}

	// qemu-img auto-detects the source format (vmdk/vdi/raw/qcow2).
	cmd := exec.Command("qemu-img", "convert", "-O", "qcow2", diskPath, imagePath)
	if out, err := cmd.CombinedOutput(); err != nil {
		return nil, fmt.Errorf("qemu-img convert to qcow2: %s: %w", string(out), err)
	}

	info, err := os.Stat(imagePath)
	if err != nil {
		return nil, err
	}
	now := time.Now()
	img := &types.Image{
		ID:          fmt.Sprintf("img-%d", now.UnixNano()),
		Name:        imageName,
		Path:        imagePath,
		SizeBytes:   info.Size(),
		Format:      "qcow2",
		Description: fmt.Sprintf("Imported from OVA (%s)", filepath.Base(ovaPath)),
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	if err := m.store.PutImage(img); err != nil {
		os.Remove(imagePath)
		return nil, err
	}

	parsed.Image = img
	return parsed, nil
}

// extractOVA unpacks a tar-format OVA into destDir and returns the path of
// the contained .ovf descriptor. Entries are flattened to their base name
// (OVAs are flat archives per spec) which also defuses path traversal.
func extractOVA(ovaPath, destDir string) (string, error) {
	f, err := os.Open(ovaPath)
	if err != nil {
		return "", fmt.Errorf("opening OVA: %w", err)
	}
	defer f.Close()

	tr := tar.NewReader(f)
	var ovfPath string
	var total int64
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", fmt.Errorf("reading OVA tar (is this a valid OVA?): %w", err)
		}
		if hdr.Typeflag != tar.TypeReg {
			continue
		}
		name := filepath.Base(filepath.Clean(hdr.Name))
		if name == "." || name == ".." || name == "/" {
			continue
		}
		total += hdr.Size
		if total > ovaMaxExtractBytes {
			return "", fmt.Errorf("OVA contents exceed the %d-byte extraction cap", int64(ovaMaxExtractBytes))
		}
		target := filepath.Join(destDir, name)
		out, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
		if err != nil {
			return "", err
		}
		if _, err := io.CopyN(out, tr, hdr.Size); err != nil && err != io.EOF {
			out.Close()
			return "", fmt.Errorf("extracting %s: %w", name, err)
		}
		out.Close()
		if strings.HasSuffix(strings.ToLower(name), ".ovf") && ovfPath == "" {
			ovfPath = target
		}
	}
	if ovfPath == "" {
		return "", fmt.Errorf("OVA contains no .ovf descriptor")
	}
	return ovfPath, nil
}

// parseOVFDescriptor pulls VM sizing plus the primary disk file reference
// out of an OVF document.
func parseOVFDescriptor(descriptor []byte) (*OVAImportResult, string, error) {
	var env ovfEnvelope
	if err := xml.Unmarshal(descriptor, &env); err != nil {
		return nil, "", fmt.Errorf("parsing OVF descriptor: %w", err)
	}

	result := &OVAImportResult{Name: firstNonEmpty(env.VirtualSystem.Name, env.VirtualSystem.ID)}

	for _, item := range env.VirtualSystem.HardwareSection.Items {
		switch item.ResourceType {
		case ovfResourceCPU:
			result.CPUs = int(item.VirtualQuantity)
		case ovfResourceMemory:
			result.RAMMB = int(memoryToMB(item.VirtualQuantity, item.AllocationUnits))
		}
	}

	var diskFileRef string
	if len(env.DiskSection.Disks) > 0 {
		d := env.DiskSection.Disks[0]
		diskFileRef = d.FileRef
		if capacity, err := strconv.ParseInt(d.Capacity, 10, 64); err == nil {
			result.DiskGB = int(capacityToGB(capacity, d.CapacityAllocationUnits))
		}
	}

	var diskHref string
	for _, f := range env.References.Files {
		if f.ID == diskFileRef || diskFileRef == "" {
			diskHref = f.Href
			break
		}
	}
	return result, diskHref, nil
}

// memoryToMB converts an OVF memory quantity to MB given its allocation
// units expression (e.g. "byte * 2^20" = MB, "byte * 2^30" = GB).
func memoryToMB(quantity int64, units string) int64 {
	switch normalizeUnits(units) {
	case "byte*2^30":
		return quantity * 1024
	case "byte*2^10":
		return quantity / 1024
	case "byte":
		return quantity / (1024 * 1024)
	default: // "byte*2^20" (MB) or unspecified — OVF's de-facto default for memory
		return quantity
	}
}

// capacityToGB converts an OVF disk capacity to whole GB (rounded up).
// Per the OVF spec an absent capacityAllocationUnits means raw bytes.
func capacityToGB(capacity int64, units string) int64 {
	var bytes int64
	switch normalizeUnits(units) {
	case "byte*2^30":
		if capacity < 1 {
			return 1
		}
		return capacity
	case "byte*2^20":
		bytes = capacity << 20
	case "byte*2^10":
		bytes = capacity << 10
	default: // "byte" or unspecified
		bytes = capacity
	}
	gb := bytes >> 30
	if bytes&((1<<30)-1) != 0 {
		gb++
	}
	if gb < 1 {
		gb = 1
	}
	return gb
}

func normalizeUnits(units string) string {
	return strings.ReplaceAll(strings.ToLower(strings.TrimSpace(units)), " ", "")
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

func sha256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func sha256File(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
