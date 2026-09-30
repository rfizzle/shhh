package safety_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rfizzle/shhh/internal/radius"
	"github.com/rfizzle/shhh/internal/safety"
)

// mkfs is a destroying row by every name it goes by, and the
// irreplaceable-target rule reads it against devices: a device is refused
// outright, and an image file or a variable is the card's to judge. A name
// that is not mkfs, or mkfs as somebody else's operand, is not flagged.
func TestMkfs_EveryNameAndItsTarget(t *testing.T) {
	w := destroyFixture(t)
	if err := os.WriteFile(filepath.Join(w.Root, "disk.img"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		command string
		refused bool
	}{
		{"mkfs.ext4 /dev/sda1", true},
		{"mkfs -t ext4 /dev/sda1", true},
		{"mkfs /dev/sda1", true},
		{"sudo mkfs.vfat -F 32 /dev/sdb1", true},
		{"/sbin/mkfs.xfs -f /dev/nvme0n1p2", true},
		{"nice -n 5 mkfs.ext4 /dev/sda1", true},
		{"if x; then mkfs.ext4 /dev/sda1; fi", true},
		{"mkfs.ext4 disk.img", false},
		{"mkfs.ext4 -F disk.img", false},
		{"mkfs.ext4 $DEV", false},
		{`mkfs.ext4 "${DEV}"`, false},
	}
	for _, c := range cases {
		if ws := safety.Check(c.command); len(ws) == 0 || ws[0].Pattern != "mkfs" {
			t.Errorf("Check(%q) = %v, want the mkfs row", c.command, ws)
		}
		got := radius.Destroys(c.command, w).Refusal()
		if c.refused && !strings.Contains(got, "a device") {
			t.Errorf("Destroys(%q).Refusal() = %q, want a device", c.command, got)
		}
		if !c.refused && got != "" {
			t.Errorf("Destroys(%q) was refused as %q; an image file is the card's to judge", c.command, got)
		}
	}
	for _, command := range []string{"mkfsx /dev/sda1", "echo mkfs", "man mkfs.ext4"} {
		if ws := safety.Check(command); len(ws) > 0 {
			t.Errorf("Check(%q) = %v, want nothing", command, ws)
		}
	}
}
