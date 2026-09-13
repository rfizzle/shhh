//go:build contract

package eval

import (
	"context"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rfizzle/shhh/internal/web"
)

// A fixture site answers the fetcher it was opened for, through the same
// tool a session fetches with — the header, the final URL and the body the
// model would see.
func TestAFixtureSiteAnswersTheFetchItWasOpenedFor(t *testing.T) {
	dir := writeSite(t, "grebe", map[string]string{"index.html": onePage})
	site, err := openSite(filepath.Join(dir, SiteDir))
	if err != nil {
		t.Fatal(err)
	}
	defer site.close()

	f := web.NewFetcher(web.FixturePolicy(site.authority))
	res, err := f.Fetch(context.Background(), site.base+"/index.html", nil)
	if err != nil {
		t.Fatalf("the site the run was opened for must be reachable: %v", err)
	}
	if res.Status != http.StatusOK || !strings.Contains(string(res.Body), "oldest first") {
		t.Errorf("status %d, body %q", res.Status, res.Body)
	}
	if got := site.pages[site.base+"/index.html"]; !strings.Contains(got, "oldest first") {
		t.Errorf("the page text a quotation is checked against = %q", got)
	}
}

// The exception is one server's, not loopback's. A second listener on the
// same address is a different origin and stays refused, which is the whole
// difference between this and setting AllowPrivate for the run.
func TestAFixtureSiteIsTheOnlyThingTheRunCanReach(t *testing.T) {
	dir := writeSite(t, "grebe", map[string]string{"index.html": onePage})
	site, err := openSite(filepath.Join(dir, SiteDir))
	if err != nil {
		t.Fatal(err)
	}
	defer site.close()
	other, err := openSite(filepath.Join(dir, SiteDir))
	if err != nil {
		t.Fatal(err)
	}
	defer other.close()

	f := web.NewFetcher(web.FixturePolicy(site.authority))
	if _, err := f.Fetch(context.Background(), other.base+"/index.html", nil); err == nil {
		t.Error("a second server on the same loopback address must still be refused")
	}
	if _, err := f.Fetch(context.Background(), "http://example.com/", nil); err == nil {
		t.Error("a run that could also read the public web is not reproducible")
	}
	// And the guard is not weakened for anybody else: the ordinary policy
	// still refuses the very site this one reaches.
	plain := web.NewFetcher(web.Policy{})
	if _, err := plain.Fetch(context.Background(), site.base+"/index.html", nil); err == nil {
		t.Error("the loopback block must still stand for a policy that named no fixture")
	}
}

// The search fixture answers on the same listener, in the shape the search
// tool's provider answers in, and ranks rather than filters — a query nobody
// anticipated must come back with pages rather than with nothing.
func TestTheSearchFixtureAnswersEveryQueryWithPages(t *testing.T) {
	dir := writeSite(t, "grebe", map[string]string{
		"index.html": onePage,
		"other.html": `<!doctype html><html><head><title>Heron</title></head><body><p>Heron is unrelated.</p></body></html>`,
	})
	site, err := openSite(filepath.Join(dir, SiteDir))
	if err != nil {
		t.Fatal(err)
	}
	defer site.close()

	s := &web.Searcher{APIKey: "fixture", Endpoint: site.base + searchPath}
	hits, err := s.Search(context.Background(), web.SearchQuery{Query: "grebe default order", Count: 5})
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 2 {
		t.Fatalf("every page should be a hit, got %d", len(hits))
	}
	if !strings.HasSuffix(hits[0].URL, "/index.html") {
		t.Errorf("the page carrying the query's words should rank first, got %s", hits[0].URL)
	}
	none, err := s.Search(context.Background(), web.SearchQuery{Query: "nothing on this site at all", Count: 5})
	if err != nil || len(none) == 0 {
		t.Errorf("a query that matches nothing must still answer: %d hits, %v", len(none), err)
	}
}

// The site a shipped case brings has to serve, and its prompt has to be able
// to name it: without the placeholder every attempt depends on the search
// fixture finding the right page first.
func TestEveryShippedResearchCaseServesItsSiteAndNamesIt(t *testing.T) {
	cases, err := Load(filepath.Join("..", "..", "evals"))
	if err != nil {
		t.Fatal(err)
	}
	found := 0
	for _, c := range cases {
		if c.Kind != KindResearch {
			continue
		}
		found++
		if !strings.Contains(c.Prompt, SitePlaceholder) {
			t.Errorf("%s: the question never names the site", c.Name)
		}
		if len(c.Facts) == 0 {
			t.Errorf("%s: no facts, so nothing decides the answer", c.Name)
		}
		site, err := openSite(c.Site)
		if err != nil {
			t.Errorf("%s: %v", c.Name, err)
			continue
		}
		resp, err := http.Get(site.base + "/")
		if err == nil {
			_, _ = io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
			if resp.StatusCode != http.StatusOK {
				t.Errorf("%s: the site's root answered %d — a case starts at index.html", c.Name, resp.StatusCode)
			}
		} else {
			t.Errorf("%s: %v", c.Name, err)
		}
		site.close()
	}
	if found == 0 {
		t.Error("the suite measures no research run")
	}
}

// writeSite lays out a research case's site the way an author would, and
// returns the case directory.
func writeSite(t *testing.T, name string, files map[string]string) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), name)
	if err := os.MkdirAll(filepath.Join(dir, SiteDir), 0o755); err != nil {
		t.Fatal(err)
	}
	for rel, body := range files {
		path := filepath.Join(dir, SiteDir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

const onePage = `<!doctype html><html><head><title>Grebe 1.4</title></head><body>
<p>Grebe 1.4 returns rows oldest first when a query names no order.</p>
</body></html>`
