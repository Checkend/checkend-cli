// Package version has the CLI version. Release builds set it with
// -ldflags "-X github.com/Checkend/checkend-cli/internal/version.Version=1.2.3". A
// `go install …/cmd/ce@v1.2.3` build has no ldflags: it takes the module version of the build.
package version

import (
	"regexp"
	"runtime/debug"
	"strings"
)

// fallback is the version of a build from source that is not a release.
const fallback = "0.2.0"

// Version is the CLI version.
var Version = ""

func init() {
	info, _ := debug.ReadBuildInfo()
	Version = resolve(Version, info)
}

var (
	release = regexp.MustCompile(`^v\d+\.\d+\.\d+(-[0-9A-Za-z.-]+)?$`)
	// pseudo is the end of a Go pseudo-version: a commit without a tag.
	pseudo = regexp.MustCompile(`\d{14}-[0-9a-f]{12}$`)
)

// resolve is the version from ldflags, else the module version of a tagged build (go install
// …@vX.Y.Z), else fallback. A pseudo-version, "(devel)" or a "+dirty" build is not a release.
func resolve(ldflags string, info *debug.BuildInfo) string {
	if ldflags != "" {
		return ldflags
	}
	if info != nil {
		v := info.Main.Version
		if release.MatchString(v) && !pseudo.MatchString(v) {
			return strings.TrimPrefix(v, "v")
		}
	}
	return fallback
}
