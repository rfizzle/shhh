package testhttp

import (
	"io"
	"net/http"
	"testing"
)

func TestRegistryDispatchesARequestWithoutAListener(t *testing.T) {
	var r Registry
	s := r.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.URL.Path != "/answer" {
			t.Errorf("path = %q", req.URL.Path)
		}
		w.Header().Set("X-Fixture", "yes")
		_, _ = w.Write([]byte("answer"))
	}))

	res, err := r.Client().Get(s.URLFor("/answer"))
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	body, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != "answer" || res.Header.Get("X-Fixture") != "yes" {
		t.Fatalf("response = %q, headers = %v", body, res.Header)
	}

	s.Close()
	if _, err := r.Client().Get(s.URL); err == nil {
		t.Fatal("a closed server must not dispatch another request")
	}
}
