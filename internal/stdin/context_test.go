package stdin

import (
	"strings"
	"testing"
)

func TestRead(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		maxChars int
		want     string
		wantErr  bool
	}{
		{
			name:     "short input within limit",
			input:    "line one\nline two\n",
			maxChars: 1000,
			want:     "line one\nline two",
		},
		{
			name:     "empty input",
			input:    "",
			maxChars: 1000,
			want:     "",
		},
		{
			name:     "truncates at limit",
			input:    "abcdefghij\nklmnopqrst\n",
			maxChars: 15,
			want:     "abcdefghij\nklmn\n[...truncated]",
		},
		{
			name:     "exact fit",
			input:    "hello",
			maxChars: 5,
			want:     "hello",
		},
		{
			name:     "truncates mid-line",
			input:    "this is a long line that exceeds the limit",
			maxChars: 10,
			want:     "this is a \n[...truncated]",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Read(strings.NewReader(tt.input), tt.maxChars)
			if (err != nil) != tt.wantErr {
				t.Fatalf("Read() error = %v, wantErr %v", err, tt.wantErr)
			}
			if got != tt.want {
				t.Errorf("Read() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestFormatPromptWithContext(t *testing.T) {
	got := FormatPromptWithContext("fix this error", "ERROR: something broke")
	want := "<context>\nERROR: something broke\n</context>\n\nfix this error"
	if got != want {
		t.Errorf("FormatPromptWithContext() = %q, want %q", got, want)
	}
}

func TestRead_LargeInput(t *testing.T) {
	line := strings.Repeat("x", 100) + "\n"
	input := strings.Repeat(line, 100)

	got, err := Read(strings.NewReader(input), 500)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.HasSuffix(got, "\n[...truncated]") {
		t.Error("expected truncation marker for large input")
	}
	content := strings.TrimSuffix(got, "\n[...truncated]")
	if len(content) > 500 {
		t.Errorf("content length %d exceeds maxChars 500", len(content))
	}
}

func TestEndToEnd_PipeWithArgs(t *testing.T) {
	input := "ERROR: connection refused\nRetry in 5s\n"
	content, err := Read(strings.NewReader(input), 1000)
	if err != nil {
		t.Fatalf("Read() error: %v", err)
	}

	prompt := FormatPromptWithContext("fix this", content)

	if !strings.Contains(prompt, "<context>") {
		t.Error("expected <context> tag in formatted prompt")
	}
	if !strings.Contains(prompt, "ERROR: connection refused") {
		t.Error("expected stdin content in formatted prompt")
	}
	if !strings.HasSuffix(prompt, "fix this") {
		t.Error("expected user prompt at end")
	}
}

func TestRead_EmptyStdin(t *testing.T) {
	got, err := Read(strings.NewReader(""), 1000)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "" {
		t.Errorf("expected empty string for empty stdin, got %q", got)
	}
}
