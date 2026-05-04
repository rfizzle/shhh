package ui

import "testing"

func TestStripFences(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"plain command", "ls -la", "ls -la"},
		{"inline backticks", "`ls -la`", "ls -la"},
		{"fenced block", "```\nls -la\n```", "ls -la"},
		{"fenced with language", "```bash\nls -la\n```", "ls -la"},
		{"fenced multiline", "```sh\nfind . -name '*.go' \\\n  -exec wc -l {} +\n```", "find . -name '*.go' \\\n  -exec wc -l {} +"},
		{"empty string", "", ""},
		{"whitespace only", "   ", ""},
		{"fenced with trailing newlines", "```\necho hello\n\n```", "echo hello"},
		{"no closing fence", "```bash\nls -la", "ls -la"},
		{"backticks inside command", "echo `date`", "echo `date`"},
		{"triple backticks alone", "```", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := StripFences(tt.input)
			if got != tt.want {
				t.Errorf("StripFences(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}
