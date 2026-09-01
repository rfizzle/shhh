package route

import "testing"

// One route, however it was spelled when it was registered, answers every
// spelling of the same request path.
func TestMatchIgnoresTrailingSlashes(t *testing.T) {
	tbl := New()
	tbl.Add("/users/42", "show-user")
	tbl.Add("/", "home")

	for _, req := range []string{"/users/42", "/users/42/", "//users//42"} {
		if got := tbl.Match(req); got != "show-user" {
			t.Errorf("Match(%q) = %q, want show-user", req, got)
		}
	}
	for _, req := range []string{"/", ""} {
		if got := tbl.Match(req); got != "home" {
			t.Errorf("Match(%q) = %q, want home", req, got)
		}
	}
}

func TestMatchIsEmptyForAnUnknownPath(t *testing.T) {
	tbl := New()
	tbl.Add("/users/42", "show-user")
	if got := tbl.Match("/nope"); got != "" {
		t.Errorf("Match on an unregistered path = %q, want empty", got)
	}
}
