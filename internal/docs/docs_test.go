package docs

import (
	"io"
	"net/http"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"
)

var (
	heading = regexp.MustCompile(`(?m)^#{1,6}\s+(.+?)\s*$`)
	drop    = regexp.MustCompile(`[^a-z0-9 _-]`)
)

// anchors are the anchors that the docs site makes from the headings of a Markdown page.
func anchors(markdown string) map[string]bool {
	out, seen := map[string]bool{}, map[string]int{}
	inCode := false
	for _, line := range strings.Split(markdown, "\n") {
		if strings.HasPrefix(line, "```") {
			inCode = !inCode
		}
		m := heading.FindStringSubmatch(line)
		if inCode || m == nil {
			continue
		}
		a := strings.ReplaceAll(drop.ReplaceAllString(strings.ToLower(m[1]), ""), " ", "-")
		if n := seen[a]; n > 0 {
			out[a+"-"+string(rune('0'+n))] = true
		} else {
			out[a] = true
		}
		seen[a]++
	}
	return out
}

func TestAnchorsFollowTheRulesOfTheSite(t *testing.T) {
	got := anchors("# Send errors\n## Back-pressure (429)\n### `POST /v1/deploys`\n## 2. Or deploy it to your Cloudflare account\n### PUBLIC_URL\n## Cloudflare\n## Cloudflare\n```\n# not a heading\n```\n")
	for _, want := range []string{"send-errors", "back-pressure-429", "post-v1deploys", "2-or-deploy-it-to-your-cloudflare-account", "public_url", "cloudflare", "cloudflare-1"} {
		if !got[want] {
			t.Errorf("no anchor %q in %v", want, got)
		}
	}
	if got["not-a-heading"] {
		t.Error("a line in a code block is a heading")
	}
}

// TestEveryLinkIsLive checks each topic against the live site: CE_DOCS_LIVE=1.
func TestEveryLinkIsLive(t *testing.T) {
	if os.Getenv("CE_DOCS_LIVE") == "" {
		t.Skip("set CE_DOCS_LIVE=1 to check the links against the live docs")
	}
	client := http.Client{Timeout: 20 * time.Second}
	pages := map[string]map[string]bool{}
	for _, topic := range All {
		page, anchor, _ := strings.Cut(string(topic), "#")
		if pages[page] == nil {
			resp, err := client.Get(Base + page + ".md")
			if err != nil {
				t.Fatal(err)
			}
			body, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			if resp.StatusCode != 200 {
				t.Errorf("%s: HTTP %d", Base+page+".md", resp.StatusCode)
				continue
			}
			pages[page] = anchors(string(body))
		}
		if anchor != "" && !pages[page][anchor] {
			t.Errorf("%s: no section #%s", URL(topic), anchor)
		}
	}
}
