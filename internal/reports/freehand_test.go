package reports

import (
	"strings"
	"testing"
)

func TestValidateFreehand_RejectsTheWholeFailureClass(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string // substring of the rejection
	}{
		{"script", `<div><script>fetch("http://x")</script></div>`, "<script> is not allowed"},
		{"style element", `<style>body{}</style>`, "<style> is not allowed"},
		{"iframe", `<iframe></iframe>`, "<iframe> is not allowed"},
		{"img", `<img>`, "<img> is not allowed"},
		{"anchor", `<a href="https://x">x</a>`, "<a> is not allowed"},
		{"foreignObject", `<svg><foreignObject></foreignObject></svg>`, "not allowed"},
		{"svg use", `<svg><use></use></svg>`, "not allowed"},
		{"event handler", `<div onclick="x()">x</div>`, `event handler "onclick"`},
		{"svg event handler", `<svg onload="x()"></svg>`, `event handler "onload"`},
		{"hex fill", `<svg><rect fill="#ff0000"/></svg>`, `literal color "#ff0000"`},
		{"hex in style", `<div style="color:#abc">x</div>`, `literal color "#abc"`},
		{"rgb in style", `<div style="color:rgb(1,2,3)">x</div>`, `"rgb" in style`},
		{"hsl in style", `<div style="background:hsl(1,2%,3%)">x</div>`, `"hsl" in style`},
		{"named color in style", `<div style="color:red">x</div>`, `"red" in color`},
		{"named fill", `<svg><rect fill="red"/></svg>`, "colors are var(--token)"},
		{"url in style", `<div style="background:url(http://x)">x</div>`, `"url" in style`},
		{"unknown token", `<div style="color:var(--nope)">x</div>`, "unknown token var(--nope)"},
		{"unknown fill token", `<svg><rect fill="var(--nope)"/></svg>`, "unknown token var(--nope)"},
		{"unknown attribute", `<div data-x="1">x</div>`, `attribute "data-x"`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ValidateFreehand(tc.in)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("ValidateFreehand = %v, want rejection containing %q", err, tc.want)
			}
		})
	}
}

func TestValidateFreehand_LegitimateDrawingSurvives(t *testing.T) {
	in := `<div style="display:flex; gap:var(--gap-2)">` +
		`<svg viewBox="0 0 100 60" width="100%" role="img">` +
		`<title>state machine</title>` +
		`<rect x="4" y="4" width="40" height="20" rx="2" fill="var(--card)" stroke="var(--rule)"/>` +
		`<line x1="44" y1="14" x2="60" y2="14" stroke="var(--del)" stroke-dasharray="2 2"/>` +
		`<text x="8" y="18" fill="var(--prose)" font-size="8">idle</text>` +
		`</svg>` +
		`<p>The dead branch is drawn in <span style="color:var(--del)">del</span>.</p></div>`
	out, err := ValidateFreehand(in)
	if err != nil {
		t.Fatalf("ValidateFreehand: %v", err)
	}
	for _, want := range []string{"<svg", "var(--del)", "<title>state machine</title>", "idle"} {
		if !strings.Contains(out, want) {
			t.Fatalf("validated markup lost %q:\n%s", want, out)
		}
	}
}

func TestValidateFreehand_IsAFixpoint(t *testing.T) {
	in := `<p>before <!-- a comment --> after</p><svg><circle cx="5" cy="5" r="3" fill="var(--series-1)"/></svg>`
	once, err := ValidateFreehand(in)
	if err != nil {
		t.Fatalf("first pass: %v", err)
	}
	if strings.Contains(once, "comment") {
		t.Fatalf("comment survived: %s", once)
	}
	twice, err := ValidateFreehand(once)
	if err != nil {
		t.Fatalf("second pass: %v", err)
	}
	if once != twice {
		t.Fatalf("validation is not a fixpoint:\nonce:  %s\ntwice: %s", once, twice)
	}
}

func TestTokenNames_ComeFromTheStylesheet(t *testing.T) {
	for _, name := range []string{"heading", "prose", "ok", "fail", "add", "del", "series-1", "series-8", "card", "rule"} {
		if !tokenNames[name] {
			t.Fatalf("token --%s missing from report.css", name)
		}
	}
	if tokenNames["nope"] {
		t.Fatal("token set invented --nope")
	}
}
