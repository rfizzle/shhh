package reports

import (
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestServer_ServesAStoredReport(t *testing.T) {
	s := openTestStore(t, t.TempDir())
	id, err := s.Put(sampleDocument(), Meta{Title: "timing", Project: "/p", Origin: "code"})
	if err != nil {
		t.Fatalf("Put: %v", err)
	}
	srv := NewServer(s)
	defer srv.Close()

	url, err := srv.URL(id)
	if err != nil {
		t.Fatalf("URL: %v", err)
	}
	if !strings.HasPrefix(url, "http://127.0.0.1:") || !strings.HasSuffix(url, "/r/"+id) {
		t.Fatalf("URL = %q", url)
	}

	res, err := http.Get(url)
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", res.StatusCode)
	}
	if got := res.Header.Get("Content-Security-Policy"); got != "default-src 'none'; style-src 'unsafe-inline'; img-src data:" {
		t.Fatalf("CSP = %q", got)
	}
	if got := res.Header.Get("Cache-Control"); got != "no-store" {
		t.Fatalf("Cache-Control = %q", got)
	}
	if got := res.Header.Get("X-Content-Type-Options"); got != "nosniff" {
		t.Fatalf("X-Content-Type-Options = %q", got)
	}
	body, _ := io.ReadAll(res.Body)
	if !strings.Contains(string(body), "Suite timing breakdown") {
		t.Fatal("served page is not the stored report")
	}
}

// A malformed id and an unknown one answer identically: a guess learns
// nothing, not even whether the shape was right.
func TestServer_GuessesLearnNothing(t *testing.T) {
	s := openTestStore(t, t.TempDir())
	srv := NewServer(s)
	defer srv.Close()
	base, err := srv.URL("rp-0123456789abcdef")
	if err != nil {
		t.Fatal(err)
	}
	root := base[:strings.Index(base, "/r/")]

	get := func(path string) (int, string) {
		res, err := http.Get(root + path)
		if err != nil {
			t.Fatalf("GET %s: %v", path, err)
		}
		defer res.Body.Close()
		b, _ := io.ReadAll(res.Body)
		return res.StatusCode, string(b)
	}

	unknownCode, unknownBody := get("/r/rp-0123456789abcdef")
	malformedCode, malformedBody := get("/r/../../etc/passwd")
	if unknownCode != http.StatusNotFound || malformedCode != http.StatusNotFound {
		t.Fatalf("codes = %d, %d, want 404s", unknownCode, malformedCode)
	}
	if unknownBody != malformedBody {
		t.Fatal("unknown and malformed ids answer differently")
	}
	if code, _ := get("/"); code != http.StatusNotFound {
		t.Fatalf("root answered %d", code)
	}
	if code, _ := get("/index.json"); code != http.StatusNotFound {
		t.Fatalf("the index answered %d over http", code)
	}
}

func TestServer_LazyAndIdempotentClose(t *testing.T) {
	s := openTestStore(t, t.TempDir())
	srv := NewServer(s)
	if srv.ln != nil {
		t.Fatal("server listened before the first URL")
	}
	if _, err := srv.URL("rp-0123456789abcdef"); err != nil {
		t.Fatal(err)
	}
	first := srv.ln
	if _, err := srv.URL("rp-0123456789abcdef"); err != nil || srv.ln != first {
		t.Fatal("second URL reopened the listener")
	}
	if err := srv.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if err := srv.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
	if _, err := srv.URL("rp-0123456789abcdef"); err == nil {
		t.Fatal("URL after Close handed out a dead link")
	}
	// And the port actually stopped answering.
	done := make(chan error, 1)
	go func() {
		_, err := http.Get("http://" + first.Addr().String() + "/r/x")
		done <- err
	}()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("closed server still answered")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("request to closed server hung")
	}
}
