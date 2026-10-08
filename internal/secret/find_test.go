package secret

import (
	"fmt"
	"strings"
	"testing"
)

// A finding is drawn on a card, kept on a receipt and sent to the model in a
// refusal, so it names the kind and the line and carries nothing else: every
// shape in the table is found where it was put, and no field of what comes
// back holds any part of the value.
func TestFind_NamesTheKindAndLineNotTheValue(t *testing.T) {
	for _, f := range fixtures {
		text := "first line\nsecond line\nKEY=" + f.text + "\nlast line\n"
		got := Find(text)
		if len(got) != 1 || got[0] != (Finding{Kind: f.kind, Line: 3}) {
			t.Errorf("%s: Find = %+v, want one %s on line 3", f.kind, got, f.kind)
			continue
		}
		if dump := fmt.Sprintf("%#v", got); strings.Contains(dump, f.text[:12]) {
			t.Errorf("%s: a finding carries the value: %s", f.kind, dump)
		}
	}
	if got := Find("github.com/rfizzle/shhh/internal/secret\ngo1.24.3 linux/amd64\n"); len(got) != 0 {
		t.Errorf("ordinary text came back with findings: %+v", got)
	}
	// A bearer JWT is one jwt, as the scrub names it, and not a jwt and a
	// bearer token besides.
	jwt := "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJzdWIiOiIxMjM0NTY3ODkwIn0.dBjftJeZ4CVP-mB92K27uhbUJU1p1r_wW1gFWFOEjXk"
	if got := Find("Authorization: Bearer " + jwt); len(got) != 1 || got[0].Kind != "jwt" {
		t.Errorf("a bearer JWT = %+v, want one jwt", got)
	}
}

// The scan a commit takes is over the lines it adds. A secret the file
// already held is in history already and is not this commit's; one written
// beside it is, and a private key whose delimiter lines read the same as an
// old one's is still a new key.
func TestFindAdded_IsOverTheLinesAChangeAdds(t *testing.T) {
	const token = "ghp_016C4C7C4C7C4C7C4C7C4C7C4C7C4C7C4C7C"
	const other = "AKIAIOSFODNN7EXAMPLE"
	before := "a\nTOKEN=" + token + "\nb\n"
	if got := FindAdded(before, before+"c\n"); len(got) != 0 {
		t.Errorf("a secret already in the file came back: %+v", got)
	}
	after := "a\nTOKEN=" + token + "\nb\nAWS=" + other + "\n"
	if got := FindAdded(before, after); len(got) != 1 || got[0] != (Finding{Kind: "aws-access-key", Line: 4}) {
		t.Errorf("FindAdded = %+v, want the one added aws-access-key on line 4", got)
	}
	if got := FindAdded("", after); len(got) != 2 {
		t.Errorf("a new file's secrets = %+v, want both", got)
	}
	oldKey := "-----BEGIN RSA PRIVATE KEY-----\nAAAA\n-----END RSA PRIVATE KEY-----\n"
	newKey := "-----BEGIN RSA PRIVATE KEY-----\nBBBB\n-----END RSA PRIVATE KEY-----\n"
	if got := FindAdded("x\n"+oldKey, "x\n"+newKey); len(got) != 1 || got[0] != (Finding{Kind: "private-key", Line: 2}) {
		t.Errorf("a replaced private key = %+v, want one on line 2", got)
	}
}
