package dryrun

import "testing"

func TestDerive(t *testing.T) {
	for _, tc := range []struct {
		name    string
		command string
		want    string
		ok      bool
	}{
		{
			name:    "rsync takes the flag behind the verb",
			command: "rsync -a src/ dest/",
			want:    "rsync --dry-run -a src/ dest/",
			ok:      true,
		},
		{
			name:    "a command already asking is left alone",
			command: "rsync -n -a src/ dest/",
			want:    "rsync -n -a src/ dest/",
			ok:      true,
		},
		{
			name:    "the flag lands behind the subcommand, not the verb",
			command: "git clean -fd",
			want:    "git clean --dry-run -fd",
			ok:      true,
		},
		{
			name:    "a git subcommand with no dry run is not offered one",
			command: "git commit -m wip",
			ok:      false,
		},
		{
			name:    "find -delete becomes the listing it answers",
			command: "find . -name '*.tmp' -delete",
			want:    "find . -name '*.tmp' -print",
			ok:      true,
		},
		{
			name:    "an -exec clause is replaced whole",
			command: "find ~/src -name node_modules -type d -prune -exec rm -rf {} +",
			want:    "find ~/src -name node_modules -type d -prune -print",
			ok:      true,
		},
		{
			name:    "an -exec closed by an escaped semicolon is replaced whole",
			command: `find . -name '*.log' -exec rm -f {} \;`,
			want:    "find . -name '*.log' -print",
			ok:      true,
		},
		{
			name:    "sed loses -i and becomes the filter it was",
			command: "sed -i 's/a/b/' notes.txt",
			want:    "sed 's/a/b/' notes.txt",
			ok:      true,
		},
		{
			name:    "sed's backup suffix form is the same -i",
			command: "sed -i.bak 's/a/b/' notes.txt",
			want:    "sed 's/a/b/' notes.txt",
			ok:      true,
		},
		{
			name:    "terraform apply becomes the command that describes it",
			command: "terraform apply -auto-approve",
			want:    "terraform plan -auto-approve",
			ok:      true,
		},
		{
			name:    "sudo does not hide the verb",
			command: "sudo apt-get remove nginx",
			want:    "sudo apt-get remove --dry-run nginx",
			ok:      true,
		},
		{
			name:    "rm has no dry run and is not given one",
			command: "rm -rf build",
			ok:      false,
		},
		{
			name:    "a read-only line rides along with a derivable one",
			command: "ls -la\nrsync -a src/ dest/",
			want:    "ls -la\nrsync --dry-run -a src/ dest/",
			ok:      true,
		},
		{
			name:    "a writing line shhh cannot rewrite stops the offer",
			command: "rsync -a src/ dest/\nrm -rf old",
			ok:      false,
		},
		{
			name:    "an unresolved line stops the offer",
			command: "rsync -a src/ dest/\nnpm run build",
			ok:      false,
		},
		{
			name:    "a command with nothing to derive is not a dry run",
			command: "ls -la",
			ok:      false,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := Derive(tc.command)
			if ok != tc.ok {
				t.Fatalf("Derive(%q) offered=%v, want %v (got %q)", tc.command, ok, tc.ok, got)
			}
			if ok && got != tc.want {
				t.Errorf("Derive(%q) = %q, want %q", tc.command, got, tc.want)
			}
		})
	}
}

func TestDeriveKeepsQuotingOutsideTheEdit(t *testing.T) {
	got, ok := Derive(`sed -i 's/one thing/another thing/g' "my notes.txt"`)
	if !ok {
		t.Fatal("expected a dry run for sed -i")
	}
	if got != `sed 's/one thing/another thing/g' "my notes.txt"` {
		t.Errorf("quoting did not survive the rewrite: %q", got)
	}
}
