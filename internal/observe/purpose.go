package observe

import (
	"encoding/json"
	"regexp"
	"strings"

	"github.com/rfizzle/shhh/internal/safety"
	"github.com/rfizzle/shhh/internal/tools"
)

// Purpose words for a command the model ran: what the shell line did, in one
// word, so the record can say how many commands were reads without holding a
// single one of them. The command text is content and the record is
// content-free by construction; the word is a code from this set and nothing
// else, which is what keeps a tool event exportable with it on
// (docs/capabilities/sessions-and-memory.md#a-command-is-recorded-by-what-it-was-for).
//
// The set is the question the record is asked, not a taxonomy of programs:
// the built-in tools answer read, search, list, write and edit without a
// card or a classifier round, so a command that did one of those is a
// command a tool could have taken, and build, vcs and other are the ones no
// built-in tool answers.
const (
	// PurposeRead: printed a file or a part of one, or a fact about it —
	// cat, head, tail, wc, sed without -i, jq.
	PurposeRead = "read"
	// PurposeSearch: looked for text inside files — grep, rg.
	PurposeSearch = "search"
	// PurposeList: named files rather than reading them — ls, find, tree.
	PurposeList = "list"
	// PurposeWrite: created, replaced, appended to, moved or removed a file —
	// a redirection to one, tee, cp, mv, rm, mkdir.
	PurposeWrite = "write"
	// PurposeEdit: changed a file where it stood — sed -i, perl -i, patch.
	PurposeEdit = "edit"
	// PurposeBuild: ran the project's toolchain — a build, a test run, a
	// formatter, a package manager.
	PurposeBuild = "build"
	// PurposeVCS: asked version control — git, gh.
	PurposeVCS = "vcs"
	// PurposeOther: anything this reading does not recognise, a script run by
	// its path, and a line that could not be read at all.
	PurposeOther = "other"
)

// purposeRank orders the words by how much the act they name did, strongest
// first. A line that ran several commands is filed under the strongest of
// them: `cat >> notes.md <<EOF` read nothing anyone asked for and appended to
// a file, and `go test ./... | tail -20` is a test run whose output was cut,
// not a read. Other sits above the three reading words because a pipe into a
// reader does not make an unknown program a read — `./run.sh | tail` ran a
// script — and below build and vcs because a line those name is theirs
// whatever else it carried.
var purposeRank = []string{
	PurposeWrite, PurposeEdit, PurposeBuild, PurposeVCS,
	PurposeOther, PurposeSearch, PurposeList, PurposeRead,
}

// Purposes is the closed set, strongest first, for a surface that draws one
// row per word.
func Purposes() []string {
	return append([]string(nil), purposeRank...)
}

// commandPurposes is what each program is, by the name it was run under. It
// is one table so a reading that should count as something else is one row:
// a verb absent from it is PurposeOther.
var commandPurposes = map[string]string{
	// read
	"cat": PurposeRead, "head": PurposeRead, "tail": PurposeRead, "less": PurposeRead,
	"more": PurposeRead, "wc": PurposeRead, "nl": PurposeRead, "tac": PurposeRead,
	"sort": PurposeRead, "uniq": PurposeRead, "cut": PurposeRead, "tr": PurposeRead,
	"column": PurposeRead, "fold": PurposeRead, "paste": PurposeRead, "join": PurposeRead,
	"comm": PurposeRead, "diff": PurposeRead, "cmp": PurposeRead, "file": PurposeRead,
	"stat": PurposeRead, "xxd": PurposeRead, "hexdump": PurposeRead, "od": PurposeRead,
	"strings": PurposeRead, "jq": PurposeRead, "jaq": PurposeRead, "gojq": PurposeRead,
	"yq": PurposeRead, "bat": PurposeRead, "zcat": PurposeRead, "gzcat": PurposeRead, "md5sum": PurposeRead,
	"sha1sum": PurposeRead, "sha256sum": PurposeRead, "shasum": PurposeRead,
	"base64": PurposeRead, "readlink": PurposeRead, "realpath": PurposeRead,
	"basename": PurposeRead, "dirname": PurposeRead, "pwd": PurposeRead,
	"which": PurposeRead, "whereis": PurposeRead, "tokei": PurposeRead,
	"sed": PurposeRead, "awk": PurposeRead, "gawk": PurposeRead,
	// search
	"grep": PurposeSearch, "egrep": PurposeSearch, "fgrep": PurposeSearch,
	"zgrep": PurposeSearch, "rg": PurposeSearch, "ag": PurposeSearch,
	"ack": PurposeSearch, "ast-grep": PurposeSearch,
	// list
	"ls": PurposeList, "tree": PurposeList, "find": PurposeList, "fd": PurposeList,
	"fdfind": PurposeList, "locate": PurposeList, "du": PurposeList,
	"exa": PurposeList, "eza": PurposeList,
	// write
	"tee": PurposeWrite, "cp": PurposeWrite, "mv": PurposeWrite, "rm": PurposeWrite,
	"rmdir": PurposeWrite, "mkdir": PurposeWrite, "touch": PurposeWrite, "ln": PurposeWrite,
	"chmod": PurposeWrite, "chown": PurposeWrite, "install": PurposeWrite,
	"truncate": PurposeWrite, "dd": PurposeWrite, "rsync": PurposeWrite,
	"zip": PurposeWrite,
	// edit
	"patch": PurposeEdit, "sd": PurposeEdit, "ed": PurposeEdit,
	// build
	"go": PurposeBuild, "gofmt": PurposeBuild, "goimports": PurposeBuild,
	"golangci-lint": PurposeBuild, "staticcheck": PurposeBuild, "make": PurposeBuild,
	"cmake": PurposeBuild, "ninja": PurposeBuild, "bazel": PurposeBuild,
	"cargo": PurposeBuild, "rustc": PurposeBuild, "rustfmt": PurposeBuild,
	"npm": PurposeBuild, "npx": PurposeBuild, "yarn": PurposeBuild, "pnpm": PurposeBuild,
	"bun": PurposeBuild, "deno": PurposeBuild, "tsc": PurposeBuild, "eslint": PurposeBuild,
	"prettier": PurposeBuild, "pytest": PurposeBuild, "tox": PurposeBuild,
	"pip": PurposeBuild, "pip3": PurposeBuild, "uv": PurposeBuild, "poetry": PurposeBuild,
	"ruff": PurposeBuild, "mypy": PurposeBuild, "black": PurposeBuild,
	"mvn": PurposeBuild, "gradle": PurposeBuild, "javac": PurposeBuild,
	"gcc": PurposeBuild, "g++": PurposeBuild, "cc": PurposeBuild, "clang": PurposeBuild,
	"dotnet": PurposeBuild, "swift": PurposeBuild, "xcodebuild": PurposeBuild,
	"bundle": PurposeBuild, "rake": PurposeBuild, "mix": PurposeBuild,
	"goreleaser": PurposeBuild,
	// vcs
	"git": PurposeVCS, "gh": PurposeVCS, "glab": PurposeVCS, "hg": PurposeVCS,
	"svn": PurposeVCS, "jj": PurposeVCS,
	// Named as other rather than left out, because each is an interpreter
	// safety.Commands pulls the code out of as a command of its own, and
	// what that code's first word looks like is not what the line did.
	// python's one read is told by its options, in flagPurposes.
	"node": PurposeOther, "ruby": PurposeOther, "perl": PurposeOther,
}

// flagPurposes is the programs whose word turns on an option: the same verb
// reads or edits depending on whether it was told to write in place. It is
// consulted before commandPurposes, and a reading that depends on how a
// program was invoked is one entry here.
var flagPurposes = map[string]func(args []string) string{
	"sed":  inPlace(PurposeRead),
	"awk":  awkInPlace,
	"gawk": awkInPlace,
	"perl": inPlace(PurposeOther),
	// The archive and compression programs write unless they were told only
	// to show what they hold: a listing names entries the way ls names
	// files, and an entry or a decompressed text sent to the output is a
	// read. Every other form extracts or compresses into a file.
	"tar":    tarPurpose,
	"unzip":  unzipPurpose,
	"gzip":   gzipPurpose,
	"gunzip": gunzipPurpose,
	// sqlite3 and python are other unless the line hands them a read a
	// built-in reader answers: a query or a schema command, a snippet that
	// reads JSON and writes nothing.
	"sqlite3": sqlitePurpose,
	"python":  pythonPurpose,
	"python3": pythonPurpose,
}

// shortOptions is every letter passed in a short option bundle, and the long
// options by name, for the programs whose word turns on a mode letter.
func shortOptions(args []string) (letters string, long []string) {
	for _, a := range args {
		switch {
		case strings.HasPrefix(a, "--"):
			name, _, _ := strings.Cut(a, "=")
			long = append(long, name)
		case len(a) > 1 && a[0] == '-':
			letters += a[1:]
		}
	}
	return letters, long
}

func hasLong(long []string, names ...string) bool {
	for _, l := range long {
		for _, n := range names {
			if l == n {
				return true
			}
		}
	}
	return false
}

// tarPurpose is a listing when tar was told t — in a short bundle, in the
// dashless bundle its first argument may be, or as --list — and nothing
// that creates, extracts or appends; every other tar is the write it was.
func tarPurpose(args []string) string {
	letters, long := shortOptions(args)
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		letters += args[0]
	}
	if strings.ContainsAny(letters, "cxruA") ||
		hasLong(long, "--create", "--extract", "--get", "--append", "--update", "--concatenate", "--catenate", "--delete") {
		return PurposeWrite
	}
	if strings.ContainsRune(letters, 't') || hasLong(long, "--list") {
		return PurposeList
	}
	return PurposeWrite
}

// unzipPurpose is a listing for -l, -v and -Z (zipinfo's mode), a read for
// -p, which sends an entry to the output rather than to a file, and
// otherwise the extraction it is.
func unzipPurpose(args []string) string {
	letters, _ := shortOptions(args)
	switch {
	case strings.ContainsAny(letters, "lvZ"):
		return PurposeList
	case strings.ContainsRune(letters, 'p'):
		return PurposeRead
	}
	return PurposeWrite
}

// gzipPurpose is a read when gzip decompresses to its output (-d with -c, in
// either spelling) or lists what a file holds (-l); compressing, or
// decompressing in place, writes a file.
func gzipPurpose(args []string) string {
	letters, long := shortOptions(args)
	decompress := strings.ContainsRune(letters, 'd') || hasLong(long, "--decompress", "--uncompress")
	if (decompress && gzipToOutput(letters, long)) || gzipListing(letters, long) {
		return PurposeRead
	}
	return PurposeWrite
}

// gunzipPurpose is gzip -d under its own name.
func gunzipPurpose(args []string) string {
	letters, long := shortOptions(args)
	if gzipToOutput(letters, long) || gzipListing(letters, long) {
		return PurposeRead
	}
	return PurposeWrite
}

func gzipToOutput(letters string, long []string) bool {
	return strings.ContainsRune(letters, 'c') || hasLong(long, "--stdout", "--to-stdout")
}

func gzipListing(letters string, long []string) bool {
	return strings.ContainsRune(letters, 'l') || hasLong(long, "--list")
}

var (
	// sqliteReads are what a sqlite3 statement may open with and still be a
	// read: a query, or a dot-command that prints the schema.
	sqliteReads = map[string]bool{
		"SELECT": true, "WITH": true, "EXPLAIN": true, "VALUES": true,
		".SCHEMA": true, ".TABLES": true, ".INDEXES": true, ".INDICES": true,
		".FULLSCHEMA": true, ".DATABASES": true,
	}
	// sqliteWrites are the words that make a sqlite3 line something other
	// than a read wherever they stand in it — a second statement that
	// changes the database, or a dot-command that writes a file, opens
	// another or runs a program.
	sqliteWrites = regexp.MustCompile(`(?i)(?:^|[^a-z0-9_.])(?:insert|update|delete|replace|create|drop|alter|attach|detach|vacuum|reindex|pragma|begin|commit|savepoint|\.(?:import|output|once|save|backup|restore|shell|system|read|excel|open|clone|recover|load|cd))(?:[^a-z0-9_]|$)`)
)

// sqlitePurpose is a read when sqlite3 is handed a database and a statement
// that only reads — a query, the schema, the table list — with none of the
// words that change a database anywhere in the line. A sqlite3 with no
// statement takes its commands from somewhere this reading cannot see, and
// one told to run something first (-cmd, -init) runs that too; both stay
// other, as every sqlite3 was.
func sqlitePurpose(args []string) string {
	var rest []string
	for i := 0; i < len(args); i++ {
		switch a := args[i]; {
		case a == "-cmd" || a == "--cmd" || a == "-init" || a == "--init":
			return PurposeOther
		case a == "-separator" || a == "--separator" || a == "-newline" || a == "--newline" ||
			a == "-nullvalue" || a == "--nullvalue" || a == "-vfs" || a == "--vfs":
			i++ // the option's own value
		case strings.HasPrefix(a, "-"):
		default:
			rest = append(rest, a)
		}
	}
	if len(rest) < 2 {
		return PurposeOther
	}
	sql := strings.TrimSpace(strings.Trim(strings.Join(rest[1:], " "), `'"`))
	first, _, _ := strings.Cut(sql, " ")
	if !sqliteReads[strings.ToUpper(first)] || sqliteWrites.MatchString(sql) {
		return PurposeOther
	}
	return PurposeRead
}

// pythonWrites are what a -c snippet could change something with: a write
// or a dump to a file, an open for writing or appending, the file system's
// own verbs, and a way to run or fetch anything else.
var pythonWrites = regexp.MustCompile(`\b(?:write|writelines|write_text|write_bytes|dump|remove|unlink|rename|replace|rmtree|rmdir|mkdir|makedirs|touch|truncate|chmod|chown|symlink|link|copy|copyfile|copytree|move|system|popen|subprocess|shutil|exec|eval|compile|__import__|socket|urllib|requests|http)\b|['"][rbt]*[wax+][rbt+]*['"]|\bmode\s*=`)

// pythonPurpose is a read when python is handed a -c snippet that reads
// JSON — json.load or json.loads — and holds nothing that could write: the
// scripting-language spelling of a jq read. Every other python is other, as
// every python was: a script run by name, a snippet that does something
// else, one this reading cannot vouch for.
func pythonPurpose(args []string) string {
	for i, a := range args {
		if a != "-c" {
			continue
		}
		code := strings.Join(args[i+1:], " ")
		if strings.Contains(code, "json.load") && !pythonWrites.MatchString(code) {
			return PurposeRead
		}
		return PurposeOther
	}
	return PurposeOther
}

// find joins the table here rather than in its literal because its reading
// reads the command its -exec runs, and that reading consults this table:
// written inline the two are an initialisation cycle.
func init() { flagPurposes["find"] = findPurpose }

// findPurpose is a listing unless find was told to act on what it found:
// -delete removes it, and -exec hands it to a command whose word counts too,
// so `find . -name '*.tmp' -exec rm {} +` is the write it is.
func findPurpose(args []string) string {
	word := PurposeList
	for i, a := range args {
		switch a {
		case "-delete":
			word = strongest(word, PurposeWrite)
		case "-exec", "-execdir", "-ok", "-okdir":
			if p, _ := commandPurpose(strings.Join(args[i+1:], " ")); p != "" {
				word = strongest(word, p)
			}
		}
	}
	return word
}

// inPlace is an edit when the options carry -i in any bundle or spelling,
// and otherwise the word given.
func inPlace(otherwise string) func([]string) string {
	return func(args []string) string {
		for _, a := range args {
			if a == "--in-place" || strings.HasPrefix(a, "--in-place=") {
				return PurposeEdit
			}
			if len(a) > 1 && a[0] == '-' && a[1] != '-' && strings.ContainsRune(a[1:], 'i') {
				return PurposeEdit
			}
		}
		return otherwise
	}
}

// awkInPlace is gawk's own spelling of an in-place edit, `-i inplace`.
func awkInPlace(args []string) string {
	for i, a := range args {
		if (a == "-i" || a == "--include") && i+1 < len(args) && args[i+1] == "inplace" {
			return PurposeEdit
		}
	}
	return PurposeRead
}

// shells hand the rest of their line to a command of its own, which
// safety.Commands offers after them; the shell is never what the line did.
var shells = map[string]bool{
	"sh": true, "bash": true, "zsh": true, "dash": true, "ksh": true, "fish": true,
}

// wrappers run the command after them and their own options, and say
// nothing about what it does.
var wrappers = map[string]bool{
	"timeout": true, "nice": true, "ionice": true, "stdbuf": true, "caffeinate": true,
}

// neutral words are the shell's own: they move the shell or print what they
// were handed, and a line of nothing else is PurposeOther rather than a read.
var neutral = map[string]bool{
	"cd": true, "pushd": true, "popd": true, "export": true, "set": true, "unset": true,
	"true": true, "false": true, ":": true, "echo": true, "printf": true, "sleep": true,
	"wait": true, "exit": true, "return": true, "test": true, "[": true, "[[": true,
	"]": true, "]]": true, "local": true, "declare": true, "read": true, "shift": true,
	"trap": true, "source": true, ".": true, "alias": true,
	// A loop's or a case's header names variables and values, not commands.
	"for": true, "case": true, "esac": true, "select": true, "function": true,
	"done": true, "fi": true, "in": true,
}

// keywords stand in front of the command they govern.
var keywords = map[string]bool{
	"if": true, "then": true, "else": true, "elif": true, "while": true,
	"until": true, "do": true, "!": true, "time": true,
}

// ToolPurpose is the purpose word a tool call is recorded with: the word for
// an execute_command's line, and "" for every other tool, which says what it
// did by its name. A command whose arguments cannot be read is other.
func ToolPurpose(tool, arguments string) string {
	if tool != tools.ExecCommandName {
		return ""
	}
	var args struct {
		Command string `json:"command"`
	}
	if err := json.Unmarshal([]byte(arguments), &args); err != nil {
		return PurposeOther
	}
	return CommandPurpose(args.Command)
}

// CommandPurpose reads a shell line into one purpose word. The line is read
// through safety.Commands — the same reading of what a line will run that
// both gates use — after the parts of a line that are not commands are taken
// out of it, and the strongest act among the commands it runs is the word
// (purposeRank). The text itself goes nowhere: what leaves is a constant.
func CommandPurpose(line string) string {
	cleaned, wrote := cleanShellLine(line)
	var found []string
	if wrote {
		found = append(found, PurposeWrite)
	}
	for _, g := range commandGroups(safety.Commands(cleaned)) {
		if p := groupPurpose(g); p != "" {
			found = append(found, p)
		}
	}
	return strongest(found...)
}

// strongest is the word among these that ranks highest, and other when there
// is none.
func strongest(words ...string) string {
	for _, p := range purposeRank {
		for _, w := range words {
			if w == p {
				return p
			}
		}
	}
	return PurposeOther
}

// commandGroups gathers the readings safety.Commands offers for one command.
// Behind an escalation it offers every word the real command could start at,
// and behind an interpreter the command it was handed, each after the one it
// came from and each a suffix of it; only one of them is the command, and a
// group is how the others are kept from counting as programs of their own.
// The suffix is judged without trailing space: a quote closing an
// interpreter's code stands alone once its parentheses are blanked, and
// trimmed to nothing it leaves a space the code handed on does not end with.
func commandGroups(cmds []string) [][]string {
	var groups [][]string
	for _, c := range cmds {
		if n := len(groups); n > 0 && strings.HasSuffix(strings.TrimRight(groups[n-1][0], " "), c) {
			groups[n-1] = append(groups[n-1], c)
			continue
		}
		groups = append(groups, []string{c})
	}
	return groups
}

// groupPurpose is the first reading in a group that names a program this
// file knows, and otherwise the first reading's own word: `sudo -u root rm
// x` is offered as `-u root rm x`, `root rm x`, `rm x` and `x`, and it is
// the rm. "" is a group of the shell's own words.
func groupPurpose(group []string) string {
	first, firstKnown := commandPurpose(group[0])
	if firstKnown {
		return first
	}
	for _, c := range group[1:] {
		if p, known := commandPurpose(c); known {
			return p
		}
	}
	return first
}

// commandPurpose is one command's word, and whether the program was one this
// file names. A shell with nothing after it that could be read is other; a
// word of the shell's own is "".
func commandPurpose(cmd string) (string, bool) {
	words := strings.Fields(cmd)
	for len(words) > 0 && keywords[words[0]] {
		words = words[1:]
	}
	for len(words) > 0 && wrappers[safety.BaseName(words[0])] {
		words = words[1:]
		for len(words) > 0 && (strings.HasPrefix(words[0], "-") || startsWithDigit(words[0])) {
			words = words[1:]
		}
	}
	if len(words) == 0 {
		return "", false
	}
	verb := safety.BaseName(words[0])
	switch {
	case neutral[verb]:
		return "", false
	case shells[verb], strings.HasPrefix(verb, "-"):
		return PurposeOther, false
	}
	if f, ok := flagPurposes[verb]; ok {
		return f(words[1:]), true
	}
	if p, ok := commandPurposes[verb]; ok {
		return p, true
	}
	return PurposeOther, false
}

func startsWithDigit(w string) bool {
	return w != "" && w[0] >= '0' && w[0] <= '9'
}

var (
	// heredocStart is a here-document's operator and its delimiter.
	heredocStart = regexp.MustCompile(`<<-?\s*(['"]?)([A-Za-z_][A-Za-z0-9_]*)(['"]?)`)
	// hereString is `<<< word`, which feeds a word rather than naming a file.
	hereString = regexp.MustCompile(`<<<\s*\S+`)
	// outputRedirect is a redirection of output and where it goes.
	outputRedirect = regexp.MustCompile(`(?:\d+|&)?>>?\|?\s*(&\d+|&-|[^\s;&|<>()]+)`)
	// inputRedirect is a redirection of input and the file it reads.
	inputRedirect = regexp.MustCompile(`\d*<\s*(?:&\d+|[^\s;&|<>()]+)`)
	// variable is a parameter expansion. safety.Commands keeps `$NAME` and
	// `${NAME}` as one word, but cuts any other braced expansion (`${x:-y}`,
	// `${#x}`) at its braces and offers what is inside as a command.
	variable = regexp.MustCompile(`\$(?:\{[^}]*\}|[A-Za-z_][A-Za-z0-9_]*|[0-9?@#*!$-])`)
	// findEnd is the `{} \;` or `{} +` that ends a find's -exec.
	findEnd = regexp.MustCompile(`\{\}|\\;`)
	// shellCode is a shell and the option that hands it a line of code.
	shellCode = regexp.MustCompile(`(?:^|[\s;&|(])(?:\S*/)?(?:sh|bash|zsh|dash|ksh|fish)(?:\s+-\S+)*\s+-[A-Za-z]*c[A-Za-z]*\s*$`)
)

// sinks are the redirection targets that write no file.
var sinks = map[string]bool{
	"/dev/null": true, "/dev/stdout": true, "/dev/stderr": true, "/dev/tty": true,
}

// cleanShellLine takes out of a line what safety.Commands would otherwise
// offer as a command — a here-document's body, a quoted pattern's `|`, a
// redirection's target, a variable's name — and reports whether the line
// redirected output into a file, which is a write whatever program it came
// from. safety.Commands over-reads on purpose because it is a gate; this
// reading is a count, and a count over-read files a grep for `a|b` as a
// program called b.
func cleanShellLine(line string) (string, bool) {
	line = dropHeredocs(line)
	line = strings.ReplaceAll(line, "\\\n", " ")
	line = neutraliseQuotes(line)
	line = hereString.ReplaceAllString(line, " ")
	wrote := false
	line = outputRedirect.ReplaceAllStringFunc(line, func(m string) string {
		target := outputRedirect.FindStringSubmatch(m)[1]
		if !strings.HasPrefix(target, "&") && !sinks[target] {
			wrote = true
		}
		return " "
	})
	line = inputRedirect.ReplaceAllString(line, " ")
	line = variable.ReplaceAllString(line, "v")
	line = findEnd.ReplaceAllString(line, " ")
	return line, wrote
}

// dropHeredocs removes each here-document's operator and its body, which is
// text handed to a command rather than commands of its own.
func dropHeredocs(line string) string {
	lines := strings.Split(line, "\n")
	var out []string
	var until []string
	for _, l := range lines {
		if len(until) > 0 {
			if strings.TrimSpace(l) == until[0] {
				until = until[1:]
			}
			continue
		}
		for _, m := range heredocStart.FindAllStringSubmatch(l, -1) {
			until = append(until, m[2])
		}
		out = append(out, heredocStart.ReplaceAllString(l, " "))
	}
	return strings.Join(out, "\n")
}

// neutraliseQuotes blanks the characters safety.Commands splits on wherever
// they sit inside quotes, so a pattern or a message is one argument rather
// than a chain. The one quoted string left alone is the code handed to a
// shell's -c, which is a line of commands and is what the shell ran.
func neutraliseQuotes(line string) string {
	b := []byte(line)
	for i := 0; i < len(b); i++ {
		q := b[i]
		if q != '\'' && q != '"' {
			continue
		}
		end := i + 1
		for end < len(b) && b[end] != q {
			if q == '"' && b[end] == '\\' {
				end++
			}
			end++
		}
		if !shellCode.MatchString(string(b[:i])) {
			for j := i + 1; j < end && j < len(b); j++ {
				if strings.IndexByte(";&|<>()`$\n{}", b[j]) >= 0 {
					b[j] = ' '
				}
			}
		}
		i = end
	}
	return string(b)
}
