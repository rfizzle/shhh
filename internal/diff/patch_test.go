package diff

import "testing"

const samplePatch = `diff --git a/internal/agent/loop.go b/internal/agent/loop.go
index 1111111..2222222 100644
--- a/internal/agent/loop.go
+++ b/internal/agent/loop.go
@@ -142,3 +142,4 @@ func run(
 	if len(calls) == 0 {
-		return results, nil
+		return results, err
+	}
 }
diff --git a/docs/new.md b/docs/new.md
new file mode 100644
index 0000000..3333333
--- /dev/null
+++ b/docs/new.md
@@ -0,0 +1,2 @@
+# Title
+body
\ No newline at end of file
diff --git a/assets/logo.png b/assets/logo.png
index 4444444..5555555 100644
Binary files a/assets/logo.png and b/assets/logo.png differ
`

func TestParsePatch_Files(t *testing.T) {
	files := ParsePatch(samplePatch)
	if len(files) != 3 {
		t.Fatalf("expected 3 files, got %d", len(files))
	}
	if files[0].Path != "internal/agent/loop.go" || files[1].Path != "docs/new.md" || files[2].Path != "assets/logo.png" {
		t.Fatalf("unexpected paths: %q %q %q", files[0].Path, files[1].Path, files[2].Path)
	}
	if !files[2].Binary || len(files[2].Hunks) != 0 {
		t.Fatal("binary file should be flagged with no hunks")
	}
}

func TestParsePatch_LineNumbers(t *testing.T) {
	files := ParsePatch(samplePatch)
	h := files[0].Hunks[0]
	if h.OldStart != 142 || h.OldCount != 3 || h.NewStart != 142 || h.NewCount != 4 {
		t.Fatalf("unexpected hunk header: %+v", h)
	}
	if len(h.Lines) != 5 {
		t.Fatalf("expected 5 lines, got %d", len(h.Lines))
	}
	del, add := h.Lines[1], h.Lines[2]
	if del.Kind != Del || del.OldNo != 143 || del.Text != "\t\treturn results, nil" {
		t.Fatalf("unexpected del line: %+v", del)
	}
	if add.Kind != Add || add.NewNo != 143 {
		t.Fatalf("unexpected add line: %+v", add)
	}
	// The paired del/add lines share a prefix/suffix, so intraline emphasis
	// marks the changed span like Compute does.
	if len(del.Emph) == 0 || len(add.Emph) == 0 {
		t.Fatal("expected intraline emphasis on the changed pair")
	}
}

func TestParsePatch_NewFileAndNoNewline(t *testing.T) {
	files := ParsePatch(samplePatch)
	h := files[1].Hunks[0]
	if len(h.Lines) != 2 {
		t.Fatalf("no-newline marker should be skipped; got %d lines", len(h.Lines))
	}
	if h.Lines[0].Kind != Add || h.Lines[0].NewNo != 1 || h.Lines[1].NewNo != 2 {
		t.Fatalf("unexpected new-file lines: %+v", h.Lines)
	}
	adds, dels := Stats(files[1].Hunks)
	if adds != 2 || dels != 0 {
		t.Fatalf("expected +2 −0, got +%d −%d", adds, dels)
	}
}

func TestParsePatch_Empty(t *testing.T) {
	if files := ParsePatch(""); files != nil {
		t.Fatalf("empty patch should return nil, got %v", files)
	}
}

func TestParsePatch_HeaderlessUnified(t *testing.T) {
	files := ParsePatch("@@ -1,2 +1,2 @@\n a\n-b\n+c\n")
	if len(files) != 1 || len(files[0].Hunks) != 1 {
		t.Fatalf("headerless patch should still parse: %+v", files)
	}
	if files[0].Path != "" {
		t.Fatalf("headerless patch has no path, got %q", files[0].Path)
	}
}
