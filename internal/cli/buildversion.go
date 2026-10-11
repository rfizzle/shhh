package cli

import (
	"regexp"
	"runtime/debug"
)

// releaseImageRepo is where the release workflow pushes the sandbox image,
// tagged with the release tag beside the digest it links into the binary.
const releaseImageRepo = "ghcr.io/rfizzle/shhh-sandbox"

var (
	// releaseTag is the version of a module build that is a release tag, and
	// neither a pseudo-version (an @master install) nor the "(devel)" a
	// source build has.
	releaseTag    = regexp.MustCompile(`^v\d+\.\d+\.\d+(-[0-9A-Za-z.]+)?$`)
	pseudoVersion = regexp.MustCompile(`-\d{14}-[0-9a-f]{12}$`)
)

// readBuildInfo is a variable so the version can be tested by injection.
var readBuildInfo = debug.ReadBuildInfo

// moduleTag is the release tag a `go install …@v0.19.0` build carries, set by
// init when version was not linked in. It is empty for a linked or dev build.
var moduleTag string

func init() {
	if v := buildVersion(version); v != version {
		version, moduleTag = v, v
	}
}

// buildVersion is the one place a version is decided, read by the update
// check and the image fallback alike: the linked-in version when there is
// one, else the module version the toolchain recorded, else "dev".
func buildVersion(linked string) string {
	if linked != "dev" && linked != "" {
		return linked
	}
	if bi, ok := readBuildInfo(); ok && bi != nil {
		if v := bi.Main.Version; releaseTag.MatchString(v) && !pseudoVersion.MatchString(v) {
			return v
		}
	}
	return "dev"
}

// fallbackImage is the tag-named image of a module build, or "" when the
// version did not come from a release tag.
func fallbackImage() string {
	if moduleTag == "" {
		return ""
	}
	return releaseImageRepo + ":" + moduleTag
}

// imageFromVersionTag reports whether the image in force is the fallback
// the version tag names, for the doctor to say so.
func imageFromVersionTag(configured string) bool {
	return configured == "" && sandboxImage == "" && fallbackImage() != ""
}
