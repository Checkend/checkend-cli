package sourcemap

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The fixtures come from the server's buildSourceMapIndex (scripts/gen-sourcemap-fixtures.mjs).
// The Go builder must give the same bytes for a valid map and the same error for a bad one.
func TestBuildGivesTheSameIndexAsTheServer(t *testing.T) {
	maps, _ := filepath.Glob("testdata/*.map")
	if len(maps) < 20 {
		t.Fatalf("only %d fixtures", len(maps))
	}
	for _, path := range maps {
		base := strings.TrimSuffix(path, ".map")
		t.Run(filepath.Base(base), func(t *testing.T) {
			input, _ := os.ReadFile(path)
			got, err := Build(input)
			if want, readErr := os.ReadFile(base + ".cksi"); readErr == nil {
				if err != nil {
					t.Fatalf("error: %v", err)
				}
				if !bytes.Equal(got, want) {
					t.Fatalf("index differs: %d bytes, want %d", len(got), len(want))
				}
				return
			}
			want, _ := os.ReadFile(base + ".error")
			if err == nil || err.Error() != strings.TrimSpace(string(want)) {
				t.Fatalf("error %v, want %q", err, strings.TrimSpace(string(want)))
			}
		})
	}
}

func TestBuildRefusesTextThatIsNotJSON(t *testing.T) {
	if _, err := Build([]byte("{not json")); err == nil || !strings.Contains(err.Error(), "JSON") {
		t.Errorf("error: %v", err)
	}
}

func BenchmarkBuildBig(b *testing.B) {
	input, _ := os.ReadFile("testdata/big.map")
	for b.Loop() {
		if _, err := Build(input); err != nil {
			b.Fatal(err)
		}
	}
}
