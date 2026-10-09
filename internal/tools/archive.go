package tools

import (
	"archive/tar"
	"archive/zip"
	"bufio"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"sort"
	"strings"
	"sync/atomic"

	"github.com/rfizzle/shhh/internal/provider"
)

// Archives and compressed files, read where they lie.
//
// A release artifact, a fixture tarball and a rotated log were read through
// `unzip -l`, `tar tzf` and `zcat | grep` — a command each, so a card or a
// classifier round each, for what is a read. The readers answer them now:
// list_directory lists an archive as it lists a directory, read_file reads one
// entry by `archive.zip!/path/in/it`, and a single-file .gz is read and
// searched as the text it holds. Nothing is extracted: every byte is streamed
// out of the archive into memory under the bounds below, and no entry name is
// ever joined to a path on disk.
//
// The input is somebody else's file, so every reading of it is bounded before
// it is spent: the decompressed bytes one call reads stop at the read ceiling,
// a stream that inflates past MaxArchiveRatio is refused with the ratio it
// reached, and an archive inside an archive is named and never opened.
// See docs/capabilities/coding-agent.md#structured-files-are-read-in-one-call.

// archiveKind is what a file's name says it is. The name decides, as it does
// for query's formats: the bytes are then checked against it, and a file whose
// bytes are not what its name says is an error, never a guess.
type archiveKind int

const (
	notArchive archiveKind = iota
	archiveZip
	archiveTar
	archiveTarGz
)

func (k archiveKind) String() string {
	switch k {
	case archiveZip:
		return "zip"
	case archiveTar:
		return "tar"
	case archiveTarGz:
		return "tar.gz"
	}
	return ""
}

func archiveKindOf(name string) archiveKind {
	lower := strings.ToLower(name)
	switch {
	case strings.HasSuffix(lower, ".zip"), strings.HasSuffix(lower, ".jar"):
		return archiveZip
	case strings.HasSuffix(lower, ".tar"):
		return archiveTar
	case strings.HasSuffix(lower, ".tar.gz"), strings.HasSuffix(lower, ".tgz"):
		return archiveTarGz
	}
	return notArchive
}

// compressedName reports whether a name is one this package would open as a
// container of some kind — an archive, or a single-file .gz. An entry with
// such a name inside an archive is a nested archive, which is listed and
// never opened.
func compressedName(name string) bool {
	return archiveKindOf(name) != notArchive || strings.HasSuffix(strings.ToLower(name), ".gz")
}

// archiveEntrySep is what separates an archive's path from the path of an
// entry inside it, the form `jar` and Java's URLs already use.
const archiveEntrySep = "!/"

// splitArchivePath splits `archive.zip!/path/in/it` into the archive on disk
// and the entry's name inside it. A path that exists as written is never
// split — a directory may be called `build!` — and neither is one whose part
// before the separator does not name an archive. The entry half is a name in
// the archive's own namespace and is never resolved against the disk.
func splitArchivePath(p string) (archive, entry string, ok bool) {
	if !strings.Contains(p, archiveEntrySep) {
		return "", "", false
	}
	if _, err := os.Lstat(p); err == nil {
		return "", "", false
	}
	for i := 0; ; {
		j := strings.Index(p[i:], archiveEntrySep)
		if j < 0 {
			return "", "", false
		}
		cut := i + j
		if archiveKindOf(p[:cut]) != notArchive {
			return p[:cut], p[cut+len(archiveEntrySep):], true
		}
		i = cut + len(archiveEntrySep)
	}
}

// errPastCeiling and archiveRatioError are the two ways a guarded stream
// stops before its end. Both are answers rather than failures: the caller
// says what was read and why it stopped.
var errPastCeiling = errors.New("past the read ceiling")

type archiveRatioError struct {
	packed, inflated int64
}

func (e *archiveRatioError) Error() string {
	return fmt.Sprintf("it inflates at %d to 1 (%s from %s compressed), past the bound of %d to 1 for a compressed file; it is refused rather than read",
		e.inflated/max(e.packed, 1), provider.HumanSize(int(e.inflated)), provider.HumanSize(int(e.packed)), MaxArchiveRatio)
}

// countingReader counts the bytes read through it: the compressed side of a
// guarded stream, which is what the ratio is measured against.
type countingReader struct {
	r io.Reader
	n int64
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.n += int64(n)
	return n, err
}

// countingReaderAt is countingReader for a zip, which is read at offsets:
// every byte the zip reader takes from the archive file is counted, so the
// compressed bytes an entry's inflation consumed are the count's growth
// while it is read, whatever the archive's table says the entry holds.
type countingReaderAt struct {
	r io.ReaderAt
	n atomic.Int64
}

func (c *countingReaderAt) ReadAt(p []byte, off int64) (int, error) {
	n, err := c.r.ReadAt(p, off)
	c.n.Add(int64(n))
	return n, err
}

// inflateGuard is the decompressed side of a stream, bounded two ways. It
// stops at limit bytes, and a read past it reports errPastCeiling rather than
// a clean end — so a caller can tell a stream that was exactly the ceiling
// from one that went on. And once more than ArchiveRatioFloor bytes have come
// out, it refuses a stream whose output is more than MaxArchiveRatio times
// the compressed bytes it has consumed; below the floor a small file of one
// repeated byte would be refused for compressing well.
//
// packed reports the compressed bytes consumed so far.
type inflateGuard struct {
	r      io.Reader
	packed func() int64
	out    int64
	limit  int64
}

func (g *inflateGuard) Read(p []byte) (int, error) {
	if g.out >= g.limit {
		var probe [1]byte
		n, err := g.r.Read(probe[:])
		if n > 0 {
			return 0, errPastCeiling
		}
		if err == nil {
			err = io.ErrNoProgress
		}
		return 0, err
	}
	if int64(len(p)) > g.limit-g.out {
		p = p[:g.limit-g.out]
	}
	n, err := g.r.Read(p)
	g.out += int64(n)
	if g.out > ArchiveRatioFloor {
		if packed := g.packed(); g.out > MaxArchiveRatio*max(packed, 1) {
			return n, &archiveRatioError{packed: packed, inflated: g.out}
		}
	}
	return n, err
}

// gzipMagic is the two bytes every gzip member opens with.
var gzipMagic = []byte{0x1f, 0x8b}

// openGzip opens a gzip stream over f behind a guard with limit bytes of
// output. A file that does not open with gzip's magic is refused in words
// rather than handed to the decompressor, which would answer with a header
// error the model cannot act on.
func openGzip(f io.Reader, name string, limit int64) (*inflateGuard, error) {
	packed := &countingReader{r: f}
	br := bufio.NewReader(packed)
	head, _ := br.Peek(2)
	if len(head) < 2 || head[0] != gzipMagic[0] || head[1] != gzipMagic[1] {
		return nil, fmt.Errorf("%s is named as gzip-compressed but its bytes are not a gzip stream", name)
	}
	zr, err := gzip.NewReader(br)
	if err != nil {
		return nil, fmt.Errorf("%s is not a readable gzip stream: %w", name, err)
	}
	return &inflateGuard{r: zr, packed: func() int64 { return packed.n }, limit: limit}, nil
}

// isGzipFile reports whether read_file and search read a file as the text it
// decompresses to: a single-file .gz whose bytes are gzip. A .tar.gz is an
// archive and is listed instead, and a file named .gz that is not gzip is
// read as the file it is.
func isGzipFile(p string) bool {
	lower := strings.ToLower(p)
	if !strings.HasSuffix(lower, ".gz") || archiveKindOf(p) != notArchive {
		return false
	}
	f, err := os.Open(p)
	if err != nil {
		return false
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return false
	}
	var head [2]byte
	if _, err := io.ReadFull(f, head[:]); err != nil {
		return false
	}
	return head[0] == gzipMagic[0] && head[1] == gzipMagic[1]
}

// inflateGzip reads a single-file .gz into memory, up to limit decompressed
// bytes. It returns what it read and the error it stopped on — errPastCeiling
// or an *archiveRatioError when a bound stopped it, nil at the stream's end.
// The compressed size is returned for the sentence that states a bound.
func inflateGzip(p string, limit int64) (data []byte, packed int64, err error) {
	f, err := os.Open(p)
	if err != nil {
		return nil, 0, fmt.Errorf("cannot read file: %w", err)
	}
	defer f.Close()
	if info, err := f.Stat(); err == nil {
		packed = info.Size()
	}
	g, err := openGzip(f, p, limit)
	if err != nil {
		return nil, packed, err
	}
	data, err = io.ReadAll(g)
	return data, packed, err
}

// readGzipForModel is readForModel for a .gz: the text it holds, or the one
// line saying why that is not what comes back.
func readGzipForModel(p string) (data []byte, notice string, err error) {
	data, packed, err := inflateGzip(p, MaxReadFileSize)
	var ratio *archiveRatioError
	switch {
	case errors.As(err, &ratio):
		return nil, fmt.Sprintf("%s is not read: %s.", p, ratio.Error()), nil
	case errors.Is(err, errPastCeiling):
		return nil, fmt.Sprintf("%s (%s compressed) decompresses to more than %s, and read_file returns no file over %s decompressed, whatever line range is asked for. Search it: search reads its first %s.",
			p, provider.HumanSize(int(packed)), provider.HumanSize(MaxReadFileSize), provider.HumanSize(MaxReadFileSize), provider.HumanSize(MaxReadFileSize)), nil
	case err != nil:
		return nil, "", fmt.Errorf("cannot decompress %s: %w", p, err)
	}
	if mediaType, text := sniffText(data[:min(len(data), SniffBytes)]); len(data) > 0 && !text {
		return nil, fmt.Sprintf("%s decompresses to a binary file (%s, %s); read_file returns text and there is none in it.",
			p, mediaType, provider.HumanSize(len(data))), nil
	}
	return data, "", nil
}

// archiveEntry is one entry as the archive states it. Nothing here was read
// from the disk under the entry's name: name is the archive's own string.
type archiveEntry struct {
	name   string
	size   int64
	packed int64 // compressed size where the format declares one, else -1
	dir    bool
	link   string // a symlink's or hard link's target
	isLink bool
	other  bool // a device, a FIFO or anything else that is not a file
	zf     *zip.File
	zipAt  *countingReaderAt // the zip's file, counting what is read of it
}

// unsafeEntryName reports whether a name would leave the directory an
// extractor put it in — an absolute path, a drive, or a `..` step — the
// names a hostile archive carries. They are listed exactly as written and
// marked, and nothing here would resolve them anyway: an entry is only ever
// looked up by its name inside the archive.
func unsafeEntryName(name string) bool {
	n := strings.ReplaceAll(name, `\`, "/")
	drive := len(n) >= 2 && n[1] == ':' && (n[0]|0x20 >= 'a' && n[0]|0x20 <= 'z')
	if strings.HasPrefix(n, "/") || drive {
		return true
	}
	for _, part := range strings.Split(n, "/") {
		if part == ".." {
			return true
		}
	}
	return false
}

// cleanEntryName is the name an entry is placed and looked up by: slashes
// only, no leading `./`, no trailing slash.
func cleanEntryName(name string) string {
	n := strings.ReplaceAll(name, `\`, "/")
	n = strings.TrimPrefix(path.Clean("/"+n), "/")
	return n
}

// archiveScan is one pass over an archive: its entries in the order it holds
// them, and where the pass stopped if it did not reach the end.
type archiveScan struct {
	kind    archiveKind
	entries []archiveEntry
	stopped error
	zipFile *os.File
}

func (s *archiveScan) Close() {
	if s.zipFile != nil {
		s.zipFile.Close()
	}
}

// scanArchive reads an archive's table of entries. A zip's table is at its
// end and states every size, so reading it decompresses nothing. A tar has no
// table: its entries are found by reading through it, which for a .tar.gz is
// decompressing it, so that pass runs under the guard and one that stops at
// a bound keeps the entries it found. visit, when set, is handed each tar
// entry with its content under the reader, and answers whether to go on.
func scanArchive(p string, visit func(e archiveEntry, r io.Reader) (bool, error)) (*archiveScan, error) {
	kind := archiveKindOf(p)
	info, err := os.Stat(p)
	if err != nil {
		return nil, fmt.Errorf("cannot read archive: %w", err)
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("%s is not a regular file", p)
	}
	s := &archiveScan{kind: kind}
	if kind == archiveZip {
		f, err := os.Open(p)
		if err != nil {
			return nil, fmt.Errorf("cannot read archive: %w", err)
		}
		at := &countingReaderAt{r: f}
		zr, err := zip.NewReader(at, info.Size())
		// A non-local name is reported as ErrInsecurePath with the reader
		// still usable; such names are what this listing marks, not a
		// reason to refuse the archive.
		if err != nil && !errors.Is(err, zip.ErrInsecurePath) {
			f.Close()
			return nil, fmt.Errorf("%s is not a readable zip archive: %w", p, err)
		}
		s.zipFile = f
		for _, f := range zr.File {
			e := archiveEntry{
				name:   f.Name,
				size:   int64(f.UncompressedSize64),
				packed: int64(f.CompressedSize64),
				dir:    f.FileInfo().IsDir(),
				zf:     f,
				zipAt:  at,
			}
			mode := f.Mode()
			switch {
			case mode&fs.ModeSymlink != 0:
				e.isLink = true
			case !e.dir && !mode.IsRegular():
				e.other = true
			}
			s.entries = append(s.entries, e)
		}
		return s, nil
	}

	f, err := os.Open(p)
	if err != nil {
		return nil, fmt.Errorf("cannot read archive: %w", err)
	}
	defer f.Close()
	// A plain tar is handed over as the file, which the tar reader seeks
	// past an entry's data on rather than reading it: nothing is
	// decompressed, so the ceiling on decompressed bytes has nothing to
	// count, and an entry read out of it is bounded by its own header's
	// size before a byte of it is read.
	var r io.Reader = f
	if kind == archiveTarGz {
		g, err := openGzip(f, p, MaxReadFileSize)
		if err != nil {
			return nil, err
		}
		r = g
	}
	tr := tar.NewReader(r)
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return s, nil
		}
		if err != nil && !errors.Is(err, tar.ErrInsecurePath) {
			if len(s.entries) == 0 && !isBound(err) {
				return nil, fmt.Errorf("%s is not a readable %s archive: %w", p, kind, err)
			}
			s.stopped = err
			return s, nil
		}
		if h.Typeflag == tar.TypeXGlobalHeader {
			// A pax global header — every `git archive` opens with one — is
			// metadata about the archive, not an entry in it.
			continue
		}
		e := archiveEntry{name: h.Name, size: h.Size, packed: -1}
		switch h.Typeflag {
		case tar.TypeDir:
			e.dir = true
		case tar.TypeReg, tar.TypeGNUSparse:
		case tar.TypeSymlink, tar.TypeLink:
			e.isLink, e.link = true, h.Linkname
		default:
			e.other = true
		}
		s.entries = append(s.entries, e)
		if visit != nil {
			more, err := visit(e, tr)
			if err != nil {
				return s, err
			}
			if !more {
				return s, nil
			}
		}
	}
}

func isBound(err error) bool {
	var ratio *archiveRatioError
	return errors.Is(err, errPastCeiling) || errors.As(err, &ratio)
}

// stoppedNote is the line a listing or a read ends with when a bound stopped
// the pass before the archive's end.
func stoppedNote(p string, err error) string {
	var ratio *archiveRatioError
	switch {
	case errors.Is(err, errRowCap):
		return fmt.Sprintf("… (stopped at %d rows, the most one listing returns; the rows above are from the entries read before it. List a directory inside it as %s%s<dir>, or a smaller depth, to see the rest)",
			MaxListEntries, p, archiveEntrySep)
	case errors.As(err, &ratio):
		return fmt.Sprintf("… (stopped: %s; the entries above are the ones read before it)", ratio.Error())
	case errors.Is(err, errPastCeiling):
		return fmt.Sprintf("… (stopped after decompressing %s, the read ceiling for one call; the entries above are the ones read before it)",
			provider.HumanSize(MaxReadFileSize))
	}
	return fmt.Sprintf("… (stopped: %s is damaged past this point: %v)", p, err)
}

// archiveNode is one place in the tree a listing draws: a directory the
// entries imply, or an entry.
type archiveNode struct {
	name     string
	entry    *archiveEntry
	children map[string]*archiveNode
}

func (n *archiveNode) child(name string) *archiveNode {
	if n.children == nil {
		n.children = map[string]*archiveNode{}
	}
	c := n.children[name]
	if c == nil {
		c = &archiveNode{name: name}
		n.children[name] = c
	}
	return c
}

func (n *archiveNode) isDir() bool {
	return n.children != nil || (n.entry != nil && n.entry.dir)
}

// archiveTree places an archive's entries as the directories they name, the
// directories a zip leaves implicit included. Names that would leave the
// archive's root are kept apart, to be listed as written.
func archiveTree(entries []archiveEntry) (root *archiveNode, unsafe []archiveEntry) {
	root = &archiveNode{}
	for i := range entries {
		e := &entries[i]
		if unsafeEntryName(e.name) {
			unsafe = append(unsafe, *e)
			continue
		}
		clean := cleanEntryName(e.name)
		if clean == "" {
			continue
		}
		n := root
		for _, part := range strings.Split(clean, "/") {
			n = n.child(part)
		}
		n.entry = e
	}
	return root, unsafe
}

// lookup finds the node at a cleaned name, or nil.
func (n *archiveNode) lookup(clean string) *archiveNode {
	if clean == "" {
		return n
	}
	for _, part := range strings.Split(clean, "/") {
		if n.children == nil || n.children[part] == nil {
			return nil
		}
		n = n.children[part]
	}
	return n
}

// errRowCap is the third way a listing's pass stops before the archive's
// end: it has found more rows than one listing returns.
var errRowCap = errors.New("past the listing's row cap")

// archiveRows counts the rows a listing will draw as the entries are found,
// so the pass over an archive can stop once there are more than one listing
// returns. A tar has no table, and the pass is the cost: a plain tar with a
// million entries is a million headers to seek between, and a .tar.gz a
// million to decompress, for a listing that shows five hundred of them.
//
// A row is a node walkArchive would draw: under the directory being listed,
// no deeper than depth, and not inside a directory it names and does not
// enter. A name that would leave the archive's root is its own row at the top
// of a listing of the whole archive. seen is what makes an entry that only
// repeats a directory an earlier one implied cost no row.
type archiveRows struct {
	start string // the cleaned directory being listed, "" for the root
	depth int
	seen  map[string]bool
	n     int
}

// add counts the rows e adds, and reports whether the rows found now fill the
// listing, its stop line included.
func (c *archiveRows) add(e archiveEntry) bool {
	if unsafeEntryName(e.name) {
		if c.start == "" {
			c.n++
		}
		return c.full()
	}
	rel := cleanEntryName(e.name)
	if c.start != "" {
		var ok bool
		if rel, ok = strings.CutPrefix(rel, c.start+"/"); !ok {
			return c.full()
		}
	}
	if rel == "" {
		return c.full()
	}
	parts := strings.Split(rel, "/")
	for i := 0; i < len(parts) && i < c.depth; i++ {
		if key := strings.Join(parts[:i+1], "/"); !c.seen[key] {
			c.seen[key] = true
			c.n++
		}
		if skipWalk(parts[i]) {
			break
		}
	}
	return c.full()
}

// full reports whether there are rows past the one the stop line takes: the
// listing keeps MaxListEntries-1 rows and says why it stopped on the last.
func (c *archiveRows) full() bool { return c.n >= MaxListEntries }

// listArchive is list_directory for an archive, or for a directory inside
// one: the same rows a directory gets — `dir: rel`, `file: rel` and a tab and
// the size — in the same order, to the same depth, with .git, node_modules
// and vendor named and not entered. The sizes are the decompressed ones.
//
// It returns no more lines than one listing returns, and a listing that
// stopped before the archive's end — at the row cap, a bound, or damage —
// spends its last line saying which: a cut applied after the rows were drawn
// would drop that line first. The pass stops reading the archive once the
// rows are more than the listing holds, so a huge tar costs the headers of
// the rows shown and not every header it has.
//
// A directory that is not there cannot be told from one still to come until a
// tar's end, but a path that runs into a file can: the first entry that names
// the listed path, or a directory above it, as something other than a
// directory settles the refusal, and the pass stops there unless an earlier
// entry already put something under that name.
func listArchive(archive, inner string, depth int) ([]string, error) {
	if compressedName(inner) || nestedArchive(inner) != "" {
		return nil, nestedRefusal(archive, inner)
	}
	rows := &archiveRows{start: cleanEntryName(inner), depth: depth, seen: map[string]bool{}}
	implied := map[string]bool{} // the directories the entries so far lie under
	s, err := scanArchive(archive, func(e archiveEntry, _ io.Reader) (bool, error) {
		if !unsafeEntryName(e.name) {
			name := cleanEntryName(e.name)
			if !e.dir && rows.start != "" && !implied[name] &&
				(name == rows.start || strings.HasPrefix(rows.start, name+"/")) {
				return false, nil
			}
			for i := 0; i < len(name); i++ {
				if name[i] == '/' {
					implied[name[:i]] = true
				}
			}
		}
		return !rows.add(e), nil
	})
	if err != nil {
		return nil, err
	}
	defer s.Close()
	if s.kind == archiveZip {
		// A zip's table has been read whole and cost nothing to decompress;
		// the rows are counted over it in the same order a tar is read in,
		// so both kinds stop at the same entry.
		for i, e := range s.entries {
			if rows.add(e) {
				s.entries = s.entries[:i+1]
				break
			}
		}
	}
	if rows.full() && s.stopped == nil {
		s.stopped = errRowCap
	}
	root, unsafe := archiveTree(s.entries)
	start := root.lookup(cleanEntryName(inner))
	switch {
	case start == nil:
		return nil, fmt.Errorf("%s has no directory %s", archive, inner)
	case !start.isDir():
		return nil, fmt.Errorf("%s!/%s is a file inside the archive; read_file reads it", archive, inner)
	}
	var lines []string
	if inner == "" || cleanEntryName(inner) == "" {
		// A name that would leave the archive's root belongs to no
		// directory in it, so it is listed at the top, as written — the
		// row a reader looking for a hostile archive most needs to see.
		for _, e := range unsafe {
			lines = append(lines, archiveRow(e.name, &e)+" (unsafe name: not resolved)")
		}
	}
	walkArchive(start, "", depth, &lines)
	if len(lines) == 0 && s.stopped == nil {
		lines = append(lines, fmt.Sprintf("(%s holds no entries)", archive))
	}
	if s.stopped != nil {
		lines = append(lines[:min(len(lines), MaxListEntries-1)], stoppedNote(archive, s.stopped))
	}
	return lines, nil
}

func walkArchive(n *archiveNode, prefix string, depth int, lines *[]string) {
	if depth < 1 {
		return
	}
	names := make([]string, 0, len(n.children))
	for name := range n.children {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		c := n.children[name]
		rel := name
		if prefix != "" {
			rel = prefix + "/" + name
		}
		if c.isDir() {
			*lines = append(*lines, "dir: "+rel)
			if depth > 1 && !skipWalk(name) {
				walkArchive(c, rel, depth-1, lines)
			}
			continue
		}
		*lines = append(*lines, archiveRow(rel, c.entry))
	}
}

// archiveRow is one file's row. A link is named with where it points and
// never followed; a nested archive is named and never opened.
func archiveRow(rel string, e *archiveEntry) string {
	switch {
	case e == nil:
		return "file: " + rel
	case e.dir:
		return "dir: " + rel
	case e.isLink && e.link != "":
		return fmt.Sprintf("file: %s (link to %s, not followed)", rel, e.link)
	case e.isLink:
		return fmt.Sprintf("file: %s (link, not followed)", rel)
	case e.other:
		return "file: " + rel
	}
	row := "file: " + rel + "\t" + provider.HumanSize(int(e.size))
	if compressedName(rel) {
		row += " (nested archive: not opened)"
	}
	return row
}

// nestedArchive returns the part of an entry path that names an archive
// before a further separator — `inner.zip` in `inner.zip!/x` — or "".
func nestedArchive(entry string) string {
	i := strings.Index(entry, archiveEntrySep)
	if i < 0 {
		return ""
	}
	if compressedName(entry[:i]) {
		return entry[:i]
	}
	return ""
}

func nestedRefusal(archive, inner string) error {
	name := inner
	if n := nestedArchive(inner); n != "" {
		name = n
	}
	return fmt.Errorf("%s!/%s is an archive inside %s; a nested archive is listed but not opened", archive, name, archive)
}

// readArchiveEntry reads one entry's bytes, or says in a sentence why it
// does not: an entry that is a directory, a link, a nested archive, or past
// a bound. Nothing is written anywhere and the entry's name is only ever
// compared with the archive's own names.
func readArchiveEntry(archive, entry string) (data []byte, notice string, err error) {
	display := archive + archiveEntrySep + entry
	if nestedArchive(entry) != "" {
		return nil, "", nestedRefusal(archive, entry)
	}
	want := entry
	wantClean := cleanEntryName(entry)
	matches := func(e archiveEntry) bool {
		return e.name == want || (!unsafeEntryName(e.name) && cleanEntryName(e.name) == wantClean)
	}

	var found *archiveEntry
	var content []byte
	var readErr error
	visit := func(e archiveEntry, r io.Reader) (bool, error) {
		if e.dir || !matches(e) {
			return true, nil
		}
		found = &e
		if e.isLink || e.other || e.size > MaxReadFileSize || compressedName(entry) {
			return false, nil
		}
		content, readErr = io.ReadAll(r)
		return false, nil
	}
	s, err := scanArchive(archive, visit)
	if err != nil {
		return nil, "", err
	}
	defer s.Close()
	if s.kind == archiveZip {
		for i := range s.entries {
			if e := s.entries[i]; !e.dir && matches(e) {
				found = &s.entries[i]
				break
			}
		}
	}
	if found == nil {
		root, _ := archiveTree(s.entries)
		if n := root.lookup(wantClean); n != nil && n.isDir() {
			return nil, "", fmt.Errorf("%s is a directory inside the archive; list_directory lists it", display)
		}
		if s.stopped != nil {
			return nil, "", fmt.Errorf("%s was not reached: %s", display, strings.TrimSuffix(strings.TrimPrefix(stoppedNote(archive, s.stopped), "… ("), ")"))
		}
		return nil, "", fmt.Errorf("%s holds no entry %s; list_directory on the archive lists what it holds", archive, entry)
	}

	switch {
	case found.isLink && found.link != "":
		return nil, fmt.Sprintf("%s is a link inside the archive to %s; links in an archive are not followed.", display, found.link), nil
	case found.isLink:
		return nil, fmt.Sprintf("%s is a link inside the archive; links in an archive are not followed.", display), nil
	case found.other:
		return nil, fmt.Sprintf("%s is not a regular file inside the archive; it has no text to return.", display), nil
	case compressedName(entry):
		return nil, fmt.Sprintf("%s is an archive inside %s (%s); a nested archive is listed but not opened.", display, archive, provider.HumanSize(int(found.size))), nil
	case found.size > MaxReadFileSize:
		return nil, fmt.Sprintf("%s is %s decompressed, and read_file returns no entry over %s, whatever line range is asked for.",
			display, provider.HumanSize(int(found.size)), provider.HumanSize(MaxReadFileSize)), nil
	}

	if found.zf != nil {
		content, readErr = readZipEntry(found)
	}
	if readErr != nil {
		var ratio *archiveRatioError
		var declared *zipUnderDeclaredError
		switch {
		case errors.As(readErr, &ratio):
			return nil, fmt.Sprintf("%s is not read: %s.", display, ratio.Error()), nil
		case errors.As(readErr, &declared):
			return nil, fmt.Sprintf("%s is not read: %s.", display, declared.Error()), nil
		case errors.Is(readErr, errPastCeiling):
			return nil, fmt.Sprintf("%s was not read whole: decompressing it passed %s, the read ceiling for one call.", display, provider.HumanSize(MaxReadFileSize)), nil
		}
		return nil, "", fmt.Errorf("cannot read %s: %w", display, readErr)
	}
	if mediaType, text := sniffText(content[:min(len(content), SniffBytes)]); len(content) > 0 && !text {
		return nil, fmt.Sprintf("%s is a binary file inside the archive (%s, %s); read_file returns text and there is none in it.",
			display, mediaType, provider.HumanSize(len(content))), nil
	}
	return content, "", nil
}

// readZipEntry opens one zip entry. Its sizes are declared in the archive's
// table, so the ratio is judged on the declaration before anything inflates.
// The table is the archive's own word, though, and a hostile one declares
// whatever passes: so the ratio is judged again on the stream, on the bytes
// the inflation actually consumed and produced, and the ceiling is held on
// it too. The zip reader stops an entry that inflates past its declared
// size, which is the one bound it enforces itself; that is refused in a
// sentence naming it rather than as a malformed archive.
func readZipEntry(e *archiveEntry) ([]byte, error) {
	if e.size > ArchiveRatioFloor && e.size > MaxArchiveRatio*max(e.packed, 1) {
		return nil, &archiveRatioError{packed: e.packed, inflated: e.size}
	}
	start := e.zipAt.n.Load()
	rc, err := e.zf.Open()
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	data, err := io.ReadAll(&inflateGuard{r: rc, packed: func() int64 { return e.zipAt.n.Load() - start }, limit: MaxReadFileSize})
	if errors.Is(err, zip.ErrFormat) {
		return nil, &zipUnderDeclaredError{declared: e.size}
	}
	return data, err
}

// zipUnderDeclaredError is an entry that inflated past the size its
// archive's table declares for it.
type zipUnderDeclaredError struct{ declared int64 }

func (e *zipUnderDeclaredError) Error() string {
	return fmt.Sprintf("it inflates past the %s its archive declares for it, and an entry is read no further than its declared size; it is refused rather than read",
		provider.HumanSize(int(e.declared)))
}

// archiveNotice is read_file's answer for an archive named whole: what it is
// and the two calls that read it.
func archiveNotice(p string, size int64) string {
	return fmt.Sprintf("%s is a %s archive (%s); list_directory lists its entries, and read_file reads one as %s%s<entry>.",
		p, archiveKindOf(p), provider.HumanSize(int(size)), p, archiveEntrySep)
}
