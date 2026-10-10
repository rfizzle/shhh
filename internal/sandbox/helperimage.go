package sandbox

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
)

// HelperLabel is the label the released image carries naming the helper
// protocol its copy of shhh speaks. An image built FROM it inherits the label
// with the binary, so it reads the same.
const HelperLabel = "dev.shhh.sandbox-exec"

// Helper readings of an image, as the doctor states them.
const (
	// HelperCarried is an image labelled with this shhh's helper protocol.
	HelperCarried = "carried"
	// HelperLacking is an image on this machine without the label, or with
	// another protocol's.
	HelperLacking = "lacking"
	// HelperUnpulled is an image the engine does not have yet, so nothing
	// can be read of it until a sandbox starts and pulls it.
	HelperUnpulled = "unpulled"
)

// ImageHelper reads whether image carries the command helper, from its
// label: a reading that looks and does not run anything, which is what a
// diagnostic may do. The label is the image's own claim; a session still
// asks the helper itself before its first turn (ProbeHelper).
func ImageHelper(ctx context.Context, eng Engine, image string) (string, error) {
	if !eng.OK {
		return "", fmt.Errorf("container engine unavailable: %s", eng.Detail)
	}
	ctx, cancel := context.WithTimeout(ctx, containerLifecycleTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, eng.Path, "image", "inspect", image)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		if isMissingImage(stderr.Bytes()) {
			return HelperUnpulled, nil
		}
		return "", fmt.Errorf("inspect %s: %v: %s", image, err, probeLine(stderr.Bytes()))
	}
	var images []struct {
		Config struct {
			Labels map[string]string `json:"Labels"`
		} `json:"Config"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &images); err != nil || len(images) == 0 {
		return "", fmt.Errorf("reading image inspect for %s: %v", image, err)
	}
	if images[0].Config.Labels[HelperLabel] == HelperVersion {
		return HelperCarried, nil
	}
	return HelperLacking, nil
}
