package version

import (
	"runtime/debug"
	"testing"
)

func TestResolve(t *testing.T) {
	info := func(v string) *debug.BuildInfo { return &debug.BuildInfo{Main: debug.Module{Version: v}} }
	for _, c := range []struct {
		name    string
		ldflags string
		info    *debug.BuildInfo
		want    string
	}{
		{"release build (ldflags)", "0.3.0", info("v0.3.0"), "0.3.0"},
		{"ldflags win", "1.2.3", info("v0.3.0"), "1.2.3"},
		{"go install @v0.3.0", "", info("v0.3.0"), "0.3.0"},
		{"go install @v0.3.0-rc.1", "", info("v0.3.0-rc.1"), "0.3.0-rc.1"},
		{"go install @latest without a tag", "", info("v0.0.0-20261012010203-abcdef123456"), fallback},
		{"after a tag, untagged commit", "", info("v0.3.1-0.20261012010203-abcdef123456"), fallback},
		{"local build", "", info("(devel)"), fallback},
		{"dirty build", "", info("v0.3.0+dirty"), fallback},
		{"no build info", "", nil, fallback},
	} {
		if got := resolve(c.ldflags, c.info); got != c.want {
			t.Errorf("%s: %q, want %q", c.name, got, c.want)
		}
	}
}
