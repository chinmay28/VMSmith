package host

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

const sampleMountInfo = `22 1 8:1 / / rw,relatime shared:1 - ext4 /dev/sda1 rw
23 22 0:5 / /proc rw,nosuid shared:2 - proc proc rw
24 22 0:21 / /run rw,nosuid shared:3 - tmpfs tmpfs rw,size=1600k
40 22 8:17 / /mnt/bulk rw,noatime shared:20 - xfs /dev/sdb1 rw
41 22 8:33 / /media/alice/My\040USB rw,nosuid shared:21 - vfat /dev/sdc1 rw
42 22 7:0 / /snap/core/1 ro,nodev shared:22 - squashfs /dev/loop0 ro
43 22 11:0 / /media/cdrom ro shared:23 - iso9660 /dev/sr0 ro
44 22 8:49 / /srv/ro rw shared:24 opt:x - ext4 /dev/sdd1 ro
45 22 8:65 / /data/nfs rw shared:25 - nfs4 server:/export rw
garbage line
`

func TestParseMountInfo(t *testing.T) {
	mounts, err := ParseMountInfo(strings.NewReader(sampleMountInfo))
	if err != nil {
		t.Fatal(err)
	}
	if len(mounts) != 9 {
		t.Fatalf("len = %d, want 9 (garbage skipped): %+v", len(mounts), mounts)
	}
	usb := mounts[4]
	if usb.Path != "/media/alice/My USB" || usb.FSType != "vfat" || usb.Source != "/dev/sdc1" || usb.ReadOnly {
		t.Fatalf("usb = %+v", usb)
	}
	if !mounts[5].ReadOnly || mounts[7].FSType != "ext4" {
		t.Fatalf("ro/optional-fields parsing wrong: %+v %+v", mounts[5], mounts[7])
	}
}

func TestPersistentMountPoints(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mountinfo")
	if err := os.WriteFile(path, []byte(sampleMountInfo), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := PersistentMountPoints(path)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"/", "/mnt/bulk", "/media/alice/My USB", "/srv/ro", "/data/nfs"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	if _, err := PersistentMountPoints(filepath.Join(t.TempDir(), "missing")); err == nil {
		t.Fatal("missing mountinfo should error")
	}
}

func TestUnescapeMountField(t *testing.T) {
	cases := map[string]string{`a\040b`: "a b", `tab\011`: "tab\t", `trail\04`: `trail\04`, `bad\9xx`: `bad\9xx`, "plain": "plain"}
	for in, want := range cases {
		if got := unescapeMountField(in); got != want {
			t.Errorf("unescapeMountField(%q) = %q, want %q", in, got, want)
		}
	}
}
