package tools

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/flate"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io/fs"
	"math/rand"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fixtureEntry is one entry of an archive a test builds. link makes it a
// symbolic link to that target; dir makes it a directory.
type fixtureEntry struct {
	name string
	body string
	link string
	dir  bool
}

func writeZip(t *testing.T, path string, entries []fixtureEntry) {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, e := range entries {
		h := &zip.FileHeader{Name: e.name, Method: zip.Deflate}
		switch {
		case e.dir:
			h.Name = strings.TrimSuffix(e.name, "/") + "/"
			h.SetMode(fs.ModeDir | 0o755)
		case e.link != "":
			h.SetMode(fs.ModeSymlink | 0o777)
		default:
			h.SetMode(0o644)
		}
		w, err := zw.CreateHeader(h)
		must(t, err)
		body := e.body
		if e.link != "" {
			body = e.link
		}
		if !e.dir {
			_, err = w.Write([]byte(body))
			must(t, err)
		}
	}
	must(t, zw.Close())
	must(t, os.WriteFile(path, buf.Bytes(), 0o644))
}

func writeTar(t *testing.T, path string, compress bool, entries []fixtureEntry) {
	t.Helper()
	var buf bytes.Buffer
	var gz *gzip.Writer
	var tw *tar.Writer
	if compress {
		gz = gzip.NewWriter(&buf)
		tw = tar.NewWriter(gz)
	} else {
		tw = tar.NewWriter(&buf)
	}
	for _, e := range entries {
		h := &tar.Header{Name: e.name, Mode: 0o644, Size: int64(len(e.body)), Typeflag: tar.TypeReg}
		switch {
		case e.dir:
			h.Typeflag, h.Size, h.Mode = tar.TypeDir, 0, 0o755
		case e.link != "":
			h.Typeflag, h.Size, h.Linkname = tar.TypeSymlink, 0, e.link
		}
		must(t, tw.WriteHeader(h))
		if h.Typeflag == tar.TypeReg {
			_, err := tw.Write([]byte(e.body))
			must(t, err)
		}
	}
	must(t, tw.Close())
	if gz != nil {
		must(t, gz.Close())
	}
	must(t, os.WriteFile(path, buf.Bytes(), 0o644))
}

func writeGzip(t *testing.T, path string, body []byte) {
	t.Helper()
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	_, err := zw.Write(body)
	must(t, err)
	must(t, zw.Close())
	must(t, os.WriteFile(path, buf.Bytes(), 0o644))
}

func listDir(t *testing.T, path string, depth int) (string, error) {
	t.Helper()
	args, _ := json.Marshal(listDirectoryArgs{Path: path, Depth: depth})
	return Execute(ListDirectoryName, args)
}

func readPath(t *testing.T, r *Recorder, path string, extra map[string]any) (string, error) {
	t.Helper()
	m := map[string]any{"path": path}
	for k, v := range extra {
		m[k] = v
	}
	args, _ := json.Marshal(m)
	return r.Execute(ReadFileName, args)
}

var fixtureTree = []fixtureEntry{
	{name: "README.md", body: "hello\n"},
	{name: "src/main.go", body: "package main\n\nfunc main() {}\n"},
	{name: "src/util/strings.go", body: "package util\n"},
}

func TestListDirectory_ListsAnArchiveLikeADirectory(t *testing.T) {
	dir := t.TempDir()
	archives := map[string]func(string){
		"fixture.zip":    func(p string) { writeZip(t, p, fixtureTree) },
		"fixture.jar":    func(p string) { writeZip(t, p, fixtureTree) },
		"fixture.tar":    func(p string) { writeTar(t, p, false, fixtureTree) },
		"fixture.tar.gz": func(p string) { writeTar(t, p, true, fixtureTree) },
		"fixture.tgz":    func(p string) { writeTar(t, p, true, fixtureTree) },
	}
	for name, build := range archives {
		t.Run(name, func(t *testing.T) {
			p := filepath.Join(dir, name)
			build(p)

			got, err := listDir(t, p, 1)
			if err != nil {
				t.Fatal(err)
			}
			if want := "file: README.md\t6 B\ndir: src"; got != want {
				t.Errorf("depth 1:\n got %q\nwant %q", got, want)
			}

			got, err = listDir(t, p, 3)
			if err != nil {
				t.Fatal(err)
			}
			want := "file: README.md\t6 B\ndir: src\nfile: src/main.go\t29 B\ndir: src/util\nfile: src/util/strings.go\t13 B"
			if got != want {
				t.Errorf("depth 3:\n got %q\nwant %q", got, want)
			}

			got, err = listDir(t, p+"!/src", 1)
			if err != nil {
				t.Fatal(err)
			}
			if want := "file: main.go\t29 B\ndir: util"; got != want {
				t.Errorf("a directory inside:\n got %q\nwant %q", got, want)
			}
		})
	}
}

func TestReadFile_ReadsOneArchiveEntryNumbered(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"fixture.zip", "fixture.tgz", "fixture.tar"} {
		t.Run(name, func(t *testing.T) {
			p := filepath.Join(dir, name)
			switch name {
			case "fixture.zip":
				writeZip(t, p, fixtureTree)
			case "fixture.tgz":
				writeTar(t, p, true, fixtureTree)
			default:
				writeTar(t, p, false, fixtureTree)
			}
			r := NewRecorder()
			got, err := readPath(t, r, p+"!/src/main.go", nil)
			if err != nil {
				t.Fatal(err)
			}
			if want := "1\tpackage main\n2\t\n3\tfunc main() {}"; got != want {
				t.Errorf("got %q, want %q", got, want)
			}

			got, err = readPath(t, r, p+"!/src/main.go", map[string]any{"tail_lines": 1})
			if err != nil {
				t.Fatal(err)
			}
			if !strings.HasPrefix(got, p+"!/src/main.go: 3 lines, 29 bytes; showing lines 3-3\n3\tfunc main() {}") {
				t.Errorf("a partial read opens with the size line, got %q", got)
			}

			// The entry is not the archive: nothing about either is on
			// the record of what the model has been shown.
			if _, ok := r.lookupSeen(p); ok {
				t.Error("reading an entry recorded the archive as seen")
			}
			if _, ok := r.lookupSeen(p + "!/src/main.go"); ok {
				t.Error("reading an entry recorded the entry's path as seen")
			}

			if _, err := readPath(t, r, p+"!/src", nil); err == nil || !strings.Contains(err.Error(), "list_directory lists it") {
				t.Errorf("a directory inside is named as one, got %v", err)
			}
			if _, err := readPath(t, r, p+"!/nope.txt", nil); err == nil || !strings.Contains(err.Error(), "holds no entry nope.txt") {
				t.Errorf("a missing entry is named, got %v", err)
			}
		})
	}
}

func TestReadFile_NamesAnArchiveReadWhole(t *testing.T) {
	p := filepath.Join(t.TempDir(), "fixture.zip")
	writeZip(t, p, fixtureTree)
	got, err := readPath(t, NewRecorder(), p, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "is a zip archive") || !strings.Contains(got, p+"!/<entry>") {
		t.Errorf("an archive read whole says how to read it, got %q", got)
	}
}

func TestReadFile_ReadsAGzipAsItsText(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "app.log.gz")
	writeGzip(t, p, []byte("started\nERROR disk full\nstopped\n"))

	r := NewRecorder()
	got, err := readPath(t, r, p, nil)
	if err != nil {
		t.Fatal(err)
	}
	if want := "1\tstarted\n2\tERROR disk full\n3\tstopped"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
	got, err = readPath(t, r, p, map[string]any{"tail_lines": 1})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(got, "3\tstopped") || !IsSizeLine(strings.SplitN(got, "\n", 2)[0]) {
		t.Errorf("a tail read of a .gz is numbered as in the text, got %q", got)
	}
	// The model was shown the decompressed text, not the file's bytes.
	if _, ok := r.lookupSeen(p); ok {
		t.Error("reading a .gz recorded the compressed file as seen")
	}

	// A file named .gz that is not gzip is the file it is.
	plain := filepath.Join(dir, "notes.gz")
	must(t, os.WriteFile(plain, []byte("just text\n"), 0o644))
	got, err = readPath(t, r, plain, nil)
	if err != nil || got != "1\tjust text" {
		t.Errorf("a .gz that is not gzip reads as itself, got %q, %v", got, err)
	}
}

func TestSearch_ReadsAGzipAsItsText(t *testing.T) {
	check := func(t *testing.T) {
		dir := t.TempDir()
		must(t, os.MkdirAll(filepath.Join(dir, "logs"), 0o755))
		gz := filepath.Join(dir, "logs", "app.log.1.gz")
		writeGzip(t, gz, []byte("started\nERROR disk full\nstopped\n"))
		must(t, os.WriteFile(filepath.Join(dir, "logs", "app.log"), []byte("ERROR today\n"), 0o644))

		out := runSearch(t, fmt.Sprintf(`{"pattern":"ERROR","path":%q,"context_lines":0}`, dir))
		if !strings.Contains(out, gz+":2: ERROR disk full") || !strings.Contains(out, "app.log:1: ERROR today") {
			t.Errorf("a directory search reads the .gz as text, got:\n%s", out)
		}
		out = runSearch(t, fmt.Sprintf(`{"pattern":"ERROR","path":%q,"context_lines":0}`, gz))
		if out != gz+":2: ERROR disk full" {
			t.Errorf("a named .gz is searched as text, got:\n%s", out)
		}
		out = runSearch(t, fmt.Sprintf(`{"pattern":"ERROR \\w+","path":%q,"only_matching":true}`, dir))
		if !strings.Contains(out, "ERROR disk") {
			t.Errorf("only_matching counts the .gz too, got:\n%s", out)
		}
	}
	t.Run("walker", func(t *testing.T) { forceWalker(t); check(t) })
	t.Run("ripgrep", func(t *testing.T) { requireRg(t); check(t) })
}

// ripgrep reads a .gz as its bytes, so a directory search leaves them out of
// ripgrep's pass and reads the ones it lists as their text.
func TestSearch_RipgrepLeavesTheGzipsToTheWalker(t *testing.T) {
	dir := t.TempDir()
	gz := filepath.Join(dir, "app.log.1.gz")
	writeGzip(t, gz, []byte("ERROR disk full\n"))
	argvFile := filepath.Join(t.TempDir(), "argv")
	fakeRg(t, fmt.Sprintf(`if [ "$1" = --files ]; then printf '%%s\0' %q; exit 0; fi; printf '%%s\n' "$@" > %s; exit 1`, gz, argvFile))

	out := runSearch(t, fmt.Sprintf(`{"pattern":"ERROR","path":%q,"context_lines":0}`, dir))
	if out != gz+":1: ERROR disk full" {
		t.Errorf("the .gz ripgrep listed is searched as text, got:\n%s", out)
	}
	argv, err := os.ReadFile(argvFile)
	must(t, err)
	if !strings.HasSuffix(strings.SplitN(string(argv), "--regexp", 2)[0], "--iglob\n!*.gz\n") {
		t.Errorf("ripgrep's own pass leaves .gz out, last of its globs, got:\n%s", argv)
	}
}

// A bomb is a stream that inflates far past what it cost to send: two
// megabytes of zeros compress to a few kilobytes.
func TestArchives_ARatioBombIsRefusedWithTheRatio(t *testing.T) {
	forceWalker(t)
	dir := t.TempDir()
	zeros := make([]byte, 2<<20)

	gz := filepath.Join(dir, "bomb.log.gz")
	writeGzip(t, gz, zeros)
	got, err := readPath(t, NewRecorder(), gz, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "to 1") || !strings.Contains(got, fmt.Sprintf("past the bound of %d to 1", MaxArchiveRatio)) {
		t.Errorf("a gzip bomb is refused with its ratio, got %q", got)
	}
	out := runSearch(t, fmt.Sprintf(`{"pattern":"x","path":%q}`, dir))
	if !strings.Contains(out, "bomb.log.gz not searched") {
		t.Errorf("a search says the bomb was not searched, got:\n%s", out)
	}

	z := filepath.Join(dir, "bomb.zip")
	writeZip(t, z, []fixtureEntry{{name: "zeros.txt", body: string(zeros)}})
	got, err = readPath(t, NewRecorder(), z+"!/zeros.txt", nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, fmt.Sprintf("past the bound of %d to 1", MaxArchiveRatio)) {
		t.Errorf("a zip bomb entry is refused with its ratio, got %q", got)
	}

	tgz := filepath.Join(dir, "bomb.tgz")
	writeTar(t, tgz, true, []fixtureEntry{{name: "zeros.txt", body: string(zeros)}, {name: "after.txt", body: "x"}})
	got, err = listDir(t, tgz, 1)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "stopped:") || !strings.Contains(got, "to 1") || strings.Contains(got, "after.txt") {
		t.Errorf("listing a tgz bomb stops at the ratio and says so, got %q", got)
	}
}

// writeRawZip writes a zip of one entry whose table states the sizes it is
// given rather than the sizes of data, which is written as it stands.
func writeRawZip(t *testing.T, path, name string, method uint16, data []byte, packed, size uint64) {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	h := &zip.FileHeader{Name: name, Method: method, CompressedSize64: packed, UncompressedSize64: size}
	h.SetMode(0o644)
	w, err := zw.CreateRaw(h)
	must(t, err)
	_, err = w.Write(data)
	must(t, err)
	must(t, zw.Close())
	must(t, os.WriteFile(path, buf.Bytes(), 0o644))
}

// A zip's table is the archive's own word. An entry that declares a size and
// a compressed size an honest ratio apart, and inflates at a thousand to one
// from bytes that are not what it declared, is judged on what the inflation
// read — and one that declares less than it holds is stopped at what it
// declared, with a sentence naming that bound.
func TestArchives_AZipEntryIsBoundedOnTheBytesItReads(t *testing.T) {
	dir := t.TempDir()
	var packed bytes.Buffer
	fw, err := flate.NewWriter(&packed, flate.BestCompression)
	must(t, err)
	_, err = fw.Write(make([]byte, 8<<20))
	must(t, err)
	must(t, fw.Close())

	lying := filepath.Join(dir, "lying.zip")
	writeRawZip(t, lying, "zeros.txt", zip.Deflate, packed.Bytes(), 1<<20, 2<<20)
	got, err := readPath(t, NewRecorder(), lying+"!/zeros.txt", nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, fmt.Sprintf("past the bound of %d to 1", MaxArchiveRatio)) {
		t.Errorf("an entry that inflates past the ratio on the bytes it read is refused with it, got %q", got)
	}

	body := bytes.Repeat([]byte("a line of text\n"), 4<<10)
	short := filepath.Join(dir, "short.zip")
	writeRawZip(t, short, "notes.txt", zip.Store, body, uint64(len(body)), 1<<10)
	got, err = readPath(t, NewRecorder(), short+"!/notes.txt", nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "inflates past the 1 KB its archive declares for it") {
		t.Errorf("an entry holding more than it declares is refused naming the declared size, got %q", got)
	}
}

// Text that inflates at an ordinary ratio but past the ceiling is cut at the
// ceiling, never read whole.
func TestArchives_DecompressionStopsAtTheReadCeiling(t *testing.T) {
	forceWalker(t)
	dir := t.TempDir()
	rng := rand.New(rand.NewSource(1))
	var body bytes.Buffer
	for body.Len() <= MaxReadFileSize+(1<<16) {
		fmt.Fprintf(&body, "line %x\n", rng.Uint64())
	}
	gz := filepath.Join(dir, "big.log.gz")
	writeGzip(t, gz, body.Bytes())

	got, err := readPath(t, NewRecorder(), gz, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "decompresses to more than 10.0 MB") || strings.Contains(got, "\t") {
		t.Errorf("a .gz past the ceiling is answered, not read, got %q", got)
	}
	out := runSearch(t, fmt.Sprintf(`{"pattern":"^line","path":%q,"files_only":true}`, gz))
	if !strings.Contains(out, "searched only in its first 10.0 MB") {
		t.Errorf("a search says what the ceiling cut, got:\n%s", out)
	}
}

// A plain tar is not decompressed, so an entry past the ceiling's worth of
// bytes is still listed and read; and a pax global header is not an entry.
func TestArchives_APlainTarIsReadPastTheCeiling(t *testing.T) {
	p := filepath.Join(t.TempDir(), "big.tar")
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	must(t, tw.WriteHeader(&tar.Header{Typeflag: tar.TypeXGlobalHeader, Name: "pax_global_header", PAXRecords: map[string]string{"comment": "abc"}}))
	big := make([]byte, MaxReadFileSize+(1<<20))
	must(t, tw.WriteHeader(&tar.Header{Name: "first.bin", Mode: 0o644, Size: int64(len(big)), Typeflag: tar.TypeReg}))
	_, err := tw.Write(big)
	must(t, err)
	must(t, tw.WriteHeader(&tar.Header{Name: "third.txt", Mode: 0o644, Size: 4, Typeflag: tar.TypeReg}))
	_, err = tw.Write([]byte("end\n"))
	must(t, err)
	must(t, tw.Close())
	must(t, os.WriteFile(p, buf.Bytes(), 0o644))

	got, err := listDir(t, p, 1)
	if err != nil {
		t.Fatal(err)
	}
	if want := "file: first.bin\t11.0 MB\nfile: third.txt\t4 B"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
	got, err = readPath(t, NewRecorder(), p+"!/third.txt", nil)
	if err != nil || got != "1\tend" {
		t.Errorf("an entry past the ceiling's worth of a plain tar reads, got %q, %v", got, err)
	}
	got, err = readPath(t, NewRecorder(), p+"!/first.bin", nil)
	if err != nil || !strings.Contains(got, "read_file returns no entry over") {
		t.Errorf("an entry over the ceiling is refused by its size, got %q, %v", got, err)
	}
}

func TestArchives_HostileNamesAreShownAndNeverResolved(t *testing.T) {
	dir := t.TempDir()
	must(t, os.MkdirAll(filepath.Join(dir, "a"), 0o755))
	// The file an extractor would have written over, beside the archive.
	must(t, os.WriteFile(filepath.Join(dir, "a", "evil.txt"), []byte("on disk\n"), 0o644))
	z := filepath.Join(dir, "a", "x.zip")
	writeZip(t, z, []fixtureEntry{
		{name: "../evil.txt", body: "archived\n"},
		{name: "/etc/passwd", body: "root\n"},
		{name: "ok.txt", body: "fine\n"},
	})

	got, err := listDir(t, z, 1)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"file: ../evil.txt\t9 B (unsafe name: not resolved)",
		"file: /etc/passwd\t5 B (unsafe name: not resolved)",
		"file: ok.txt\t5 B",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("listing lacks %q:\n%s", want, got)
		}
	}
	got, err = readPath(t, NewRecorder(), z+"!/../evil.txt", nil)
	if err != nil {
		t.Fatal(err)
	}
	if got != "1\tarchived" {
		t.Errorf("a `..` entry is read out of the archive, not off the disk; got %q", got)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "a", "evil.txt")); string(b) != "on disk\n" {
		t.Errorf("the file beside the archive changed: %q", b)
	}
	if _, err := os.Stat(filepath.Join(dir, "evil.txt")); !os.IsNotExist(err) {
		t.Errorf("reading the archive wrote outside it: %v", err)
	}
}

func TestArchives_ANestedArchiveIsListedNotOpened(t *testing.T) {
	dir := t.TempDir()
	inner := filepath.Join(dir, "inner.zip")
	writeZip(t, inner, fixtureTree)
	body, err := os.ReadFile(inner)
	must(t, err)
	outer := filepath.Join(dir, "outer.zip")
	writeZip(t, outer, []fixtureEntry{{name: "inner.zip", body: string(body)}, {name: "log.gz", body: "x"}})

	got, err := listDir(t, outer, 1)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "file: inner.zip\t") || !strings.Contains(got, "(nested archive: not opened)") {
		t.Errorf("a nested archive is listed and marked, got %q", got)
	}
	got, err = readPath(t, NewRecorder(), outer+"!/inner.zip", nil)
	if err != nil || !strings.Contains(got, "a nested archive is listed but not opened") {
		t.Errorf("reading a nested archive whole is a notice, got %q, %v", got, err)
	}
	for _, p := range []string{outer + "!/inner.zip!/README.md", outer + "!/log.gz"} {
		out, err := readPath(t, NewRecorder(), p, nil)
		if (err == nil || !strings.Contains(err.Error(), "not opened")) && !strings.Contains(out, "not opened") {
			t.Errorf("%s is not opened, got %q, %v", p, out, err)
		}
	}
	if _, err := listDir(t, outer+"!/inner.zip", 1); err == nil || !strings.Contains(err.Error(), "not opened") {
		t.Errorf("listing into a nested archive is refused, got %v", err)
	}
}

func TestArchives_ALinkEntryIsNamedNotFollowed(t *testing.T) {
	dir := t.TempDir()
	secret := filepath.Join(dir, "secret.txt")
	must(t, os.WriteFile(secret, []byte("do not show\n"), 0o644))
	for _, name := range []string{"links.tar", "links.zip"} {
		p := filepath.Join(dir, name)
		entries := []fixtureEntry{{name: "to-secret", link: secret}}
		if name == "links.zip" {
			writeZip(t, p, entries)
		} else {
			writeTar(t, p, false, entries)
		}
		got, err := listDir(t, p, 1)
		if err != nil || !strings.Contains(got, "not followed") {
			t.Errorf("%s: a link entry is marked, got %q, %v", name, got, err)
		}
		got, err = readPath(t, NewRecorder(), p+"!/to-secret", nil)
		if err != nil || strings.Contains(got, "do not show") || !strings.Contains(got, "not followed") {
			t.Errorf("%s: a link entry is not followed, got %q, %v", name, got, err)
		}
	}
}

func TestArchives_BytesThatAreNotTheirNameAreAnError(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"fake.tgz", "fake.zip", "fake.tar.gz"} {
		p := filepath.Join(dir, name)
		must(t, os.WriteFile(p, []byte("this is not an archive at all\n"), 0o644))
		if _, err := listDir(t, p, 1); err == nil {
			t.Errorf("%s: listing a fake archive should fail in words", name)
		}
		if _, err := readPath(t, NewRecorder(), p+"!/x", nil); err == nil {
			t.Errorf("%s: reading into a fake archive should fail in words", name)
		}
	}
	// A truncated gzip is an error, not a crash and not a silent empty read.
	p := filepath.Join(dir, "cut.log.gz")
	writeGzip(t, p, bytes.Repeat([]byte("some line of a log\n"), 500))
	b, err := os.ReadFile(p)
	must(t, err)
	must(t, os.WriteFile(p, b[:len(b)/2], 0o644))
	if _, err := readPath(t, NewRecorder(), p, nil); err == nil {
		t.Error("a truncated gzip should be an error")
	}
}

func TestSplitArchivePath(t *testing.T) {
	dir := t.TempDir()
	// A real directory whose name has the separator in it is not split.
	must(t, os.MkdirAll(filepath.Join(dir, "build.zip!", "x"), 0o755))
	must(t, os.WriteFile(filepath.Join(dir, "build.zip!", "x", "f"), nil, 0o644))
	cases := []struct {
		in, archive, entry string
		ok                 bool
	}{
		{"a.zip!/b/c.txt", "a.zip", "b/c.txt", true},
		{"dir!/a.tgz!/x", "dir!/a.tgz", "x", true},
		{"a.txt!/b", "", "", false},
		{"plain/path", "", "", false},
		{filepath.Join(dir, "build.zip!", "x", "f"), "", "", false},
	}
	for _, c := range cases {
		a, e, ok := splitArchivePath(c.in)
		if a != c.archive || e != c.entry || ok != c.ok {
			t.Errorf("splitArchivePath(%q) = %q, %q, %v", c.in, a, e, ok)
		}
	}
}

// The description states the bounds the code enforces; this holds the two
// together.
func TestArchives_TheDescriptionsNameTheBounds(t *testing.T) {
	read := readFile.Tool.Description
	if !strings.Contains(read, "`archive.zip!/path/in/it`") {
		t.Error("read_file's description names the !/ form")
	}
	if !strings.Contains(read, fmt.Sprintf("at most %d MB", MaxReadFileSize>>20)) ||
		!strings.Contains(read, fmt.Sprintf("past %d to 1", MaxArchiveRatio)) {
		t.Errorf("read_file's description names the ceiling and the ratio: %q", read)
	}
	if !strings.Contains(listDirectory.Tool.Description, "lists like a directory") {
		t.Error("list_directory's description says an archive lists like a directory")
	}
}
