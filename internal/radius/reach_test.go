package radius

import "testing"

func TestReach(t *testing.T) {
	for _, tc := range []struct {
		name    string
		command string
		want    string
	}{
		{
			name:    "a resolved read says so on all three facets",
			command: "grep -r listen .",
			want:    "read-only · no network · no sudo",
		},
		{
			name:    "a resolved write names the path it writes",
			command: "touch notes.txt",
			want:    "writes notes.txt · no network · no sudo",
		},
		{
			name:    "several writes are counted rather than listed",
			command: "touch a.txt b.txt c.txt",
			want:    "writes 3 paths · no network · no sudo",
		},
		{
			name:    "an unresolved verb never reads as read-only",
			command: "npm run build",
			want:    "writes unknown · network unknown · no sudo",
		},
		{
			name:    "a network verb is named, and its writes stay unknown",
			command: "curl https://example.com",
			want:    "writes unknown · network · no sudo",
		},
		{
			name:    "a git subcommand that stays local is not the network",
			command: "git status",
			want:    "read-only · no network · no sudo",
		},
		{
			name:    "a git subcommand that leaves is",
			command: "git push origin main",
			want:    "writes unknown · network · no sudo",
		},
		{
			name:    "escalation is always knowable and always said",
			command: "sudo rm -rf /var/cache/x",
			want:    "writes /var/cache/x · no network · sudo",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := Outline(tc.command).Reach(); got != tc.want {
				t.Errorf("Outline(%q).Reach() = %q, want %q", tc.command, got, tc.want)
			}
		})
	}
}

func TestReachKeepsUnknownBesideWhatItResolved(t *testing.T) {
	// One segment resolves and the other does not; the line has to carry both
	// rather than letting the resolved half stand for the whole command.
	got := Outline("touch notes.txt && npm run build").Reach()
	want := "writes notes.txt, plus unknown · network unknown · no sudo"
	if got != want {
		t.Errorf("Reach() = %q, want %q", got, want)
	}
}
