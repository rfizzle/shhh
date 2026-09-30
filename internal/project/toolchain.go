package project

// A checkout's toolchain declaration: the tools its own checks need that the
// sandbox image does not carry, named once in the checkout so that a
// contained session can build and test it. The file is command text that
// will run, so it is in the trust set and is read only where the checkout is
// trusted; and it is the key a prepared image is kept under, so every line
// has to mean the same thing every time it is read.
// See docs/capabilities/containment.md#a-checkout-declares-the-toolchain-its-work-needs.

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/BurntSushi/toml"
)

// Toolchain is a checkout's declaration as read: every entry checked, the
// hosts in the one spelling a proxy compares them in.
type Toolchain struct {
	// Packages are names in the base image's own package index.
	Packages []string
	// Install are command lines, one install each, every one pinned to an
	// exact version.
	Install []string
	// Hosts are the registries an install run on the host may reach.
	Hosts []string
	// Check are the binaries the work expects to find on PATH.
	Check []string
	// Digest identifies the file by its bytes: what a prepared image is
	// kept under, taken from the same read the entries were parsed from.
	Digest string
}

// maxToolchainBytes bounds the read. A declaration is a few dozen lines; the
// bound is there so a checkout cannot make every session start by reading a
// file of any size, not because a real one comes near it.
const maxToolchainBytes = 64 << 10

// LoadToolchain reads the toolchain the checkout under t.Root declares, and
// reports whether there was one to load. A checkout the person has not
// trusted loads nothing and is no error — withholding is a diagnostic, and
// t.Withheld is what names it — so a broken file in a checkout nobody has
// answered for cannot stop a session either.
//
// A file that is there and trusted is read whole or not at all: an entry
// that cannot be checked, a key nobody reads, or an install line whose pin
// cannot be read is refused naming the file and the entry, rather than
// dropped. A declaration that silently lost a line would prepare an image
// without the tool the line was for, and the first anyone would hear of it
// is a check failing inside the container.
func LoadToolchain(t Trust) (Toolchain, bool, error) {
	if !t.Allows() || t.Root == "" {
		return Toolchain{}, false, nil
	}
	path := filepath.Join(t.Root, filepath.FromSlash(ToolchainFile))
	info, err := os.Lstat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return Toolchain{}, false, nil
	}
	if err != nil {
		return Toolchain{}, false, fmt.Errorf("%s: %w", ToolchainFile, err)
	}
	// The trust answer records a symlink as the link, not as what it points
	// at, so reading through one would load a file whose edits are never
	// told. The declaration has to be a file in the checkout.
	if !info.Mode().IsRegular() {
		return Toolchain{}, false, fmt.Errorf("%s is not a regular file: the declaration has to be a file in the checkout, not a link or a directory", ToolchainFile)
	}
	if info.Size() > maxToolchainBytes {
		return Toolchain{}, false, fmt.Errorf("%s is %d bytes; a declaration is at most %d", ToolchainFile, info.Size(), maxToolchainBytes)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return Toolchain{}, false, fmt.Errorf("%s: %w", ToolchainFile, err)
	}
	tc, err := parseToolchain(data)
	if err != nil {
		return Toolchain{}, false, fmt.Errorf("%s: %w", ToolchainFile, err)
	}
	return tc, true, nil
}

// parseToolchain is the file's grammar, apart from where the bytes came from.
func parseToolchain(data []byte) (Toolchain, error) {
	var raw struct {
		Packages []string `toml:"packages"`
		Install  []string `toml:"install"`
		Hosts    []string `toml:"hosts"`
		Check    []string `toml:"check"`
	}
	meta, err := toml.Decode(string(data), &raw)
	if err != nil {
		return Toolchain{}, err
	}
	// A misspelt key would otherwise be a list nobody reads: `instal` and
	// its lines gone without a word.
	if left := meta.Undecoded(); len(left) > 0 {
		return Toolchain{}, fmt.Errorf("unknown key %q: the declaration takes packages, install, hosts and check", left[0].String())
	}
	sum := sha256.Sum256(data)
	tc := Toolchain{Digest: hex.EncodeToString(sum[:])}
	for i, p := range raw.Packages {
		p = strings.TrimSpace(p)
		if !packageName.MatchString(p) {
			return Toolchain{}, fmt.Errorf("packages[%d] %q is not a package name: name the package alone, or as name=version", i, p)
		}
		tc.Packages = append(tc.Packages, p)
	}
	for i, line := range raw.Install {
		line = strings.TrimSpace(line)
		if err := pinnedInstall(line); err != nil {
			return Toolchain{}, fmt.Errorf("install[%d] %q: %w", i, line, err)
		}
		tc.Install = append(tc.Install, line)
	}
	seen := map[string]bool{}
	for i, h := range raw.Hosts {
		n := normalHost(h)
		if !validHost(n) {
			return Toolchain{}, fmt.Errorf("hosts[%d] %q is not a host name: name the host alone — no scheme, port, path or wildcard", i, strings.TrimSpace(h))
		}
		if !seen[n] {
			seen[n] = true
			tc.Hosts = append(tc.Hosts, n)
		}
	}
	for i, c := range raw.Check {
		c = strings.TrimSpace(c)
		if c == "" || strings.ContainsAny(c, "/\\ \t") {
			return Toolchain{}, fmt.Errorf("check[%d] %q is not a program name: name the binary as it is found on PATH", i, c)
		}
		tc.Check = append(tc.Check, c)
	}
	return tc, nil
}

// packageName is an apk package, alone or at one exact version — the base
// image is Wolfi, so its index is apk's.
var packageName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._+-]*(=[A-Za-z0-9._+-]+)?$`)

// normalHost and validHost are the proxy's own reading of a host, so a
// declared registry is one the host list will take. They are spelled again
// here because the sandbox imports this package and not the other way round;
// a test in the sandbox holds the two to the same answers.
func normalHost(h string) string {
	h = strings.ToLower(strings.TrimSpace(h))
	h = strings.TrimSuffix(strings.TrimPrefix(h, "["), "]")
	return strings.TrimSuffix(h, ".")
}

func validHost(h string) bool {
	if h == "" {
		return false
	}
	if net.ParseIP(h) != nil {
		return true
	}
	for label := range strings.SplitSeq(h, ".") {
		if label == "" || strings.HasPrefix(label, "-") || strings.HasSuffix(label, "-") {
			return false
		}
		for _, r := range label {
			letter, digit := r >= 'a' && r <= 'z', r >= '0' && r <= '9'
			if !letter && !digit && r != '-' && r != '_' {
				return false
			}
		}
	}
	return true
}

// shellSyntax is everything that would make a line more than one command
// with words: a chain, a pipe, a redirection, a substitution, a quote. A
// line carrying any of it is refused before its installer is looked for,
// because whether `curl … | sh` is pinned is a question about a script
// nobody declared.
const shellSyntax = ";&|<>$`'\"\\(){}!#\n"

// pinnedInstall refuses an install line whose meaning could move while the
// file's bytes stay the same. A prepared image is kept under those bytes, so
// a line that installs "whatever is newest" would leave the cache serving
// last month's tool under this month's declaration — the one thing a cache
// keyed on content may not do. What is accepted is what can be read: one
// command of an installer this knows, every package it names at one exact
// version. Anything else is refused rather than guessed at, since a line
// whose pin cannot be read is a line whose pin cannot be vouched for.
// See docs/capabilities/containment.md#a-checkout-declares-the-toolchain-its-work-needs.
func pinnedInstall(line string) error {
	if line == "" {
		return errors.New("an empty install line installs nothing")
	}
	if strings.ContainsAny(line, shellSyntax) {
		return errors.New("one command per line, with no shell syntax: a chain, pipe, redirection, substitution or quote hides what is installed")
	}
	words := strings.Fields(line)
	// Leading NAME=value assignments are the command's environment
	// (CGO_ENABLED=0 go install …), not part of what it installs.
	for len(words) > 0 && envAssign.MatchString(words[0]) {
		words = words[1:]
	}
	for _, in := range installers {
		if len(words) < len(in.lead) || !sameWords(words[:len(in.lead)], in.lead) {
			continue
		}
		return in.check(words[len(in.lead):])
	}
	return fmt.Errorf("no installer this can read a pin from; a line is one of %s", installerNames())
}

var envAssign = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*=`)

func sameWords(a, b []string) bool {
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// installer is one command whose arguments this can read a pin from.
type installer struct {
	// lead are the words that begin the command.
	lead []string
	// takes are the flags whose value is the next word.
	takes map[string]bool
	// refuse are the flags that install from something the line does not
	// name — a file, a path, a branch — and why each is refused.
	refuse map[string]string
	// version are the flags whose value is the version of the one package
	// the line names, where the installer spells a pin that way.
	version map[string]bool
	// pin splits one argument into its package and version, reporting
	// whether the argument names a package this installer can install.
	pin func(arg string) (name, version string, ok bool)
	// exact reports whether a version is one version and not a range, a
	// tag or a branch.
	exact func(v string) bool
	// example is how a pinned argument is written, for the refusal.
	example string
}

func (in installer) name() string { return strings.Join(in.lead, " ") }

func (in installer) check(args []string) error {
	var pkgs []string
	flagVersion := ""
	for i := 0; i < len(args); i++ {
		a := args[i]
		if !strings.HasPrefix(a, "-") {
			pkgs = append(pkgs, a)
			continue
		}
		flag, value, inline := strings.Cut(a, "=")
		if why, ok := in.refuse[flag]; ok {
			return fmt.Errorf("%s %s", flag, why)
		}
		// A short flag may carry its value attached (`-rreqs.txt`) or sit in
		// a bundle (`-vU`), and pip's parser reads an unambiguous prefix of a
		// long flag as the flag: none of those is the spelling in the table.
		if !strings.HasPrefix(flag, "--") {
			for _, c := range flag[1:] {
				short := "-" + string(c)
				if why, ok := in.refuse[short]; ok {
					return fmt.Errorf("%s %s", a, why)
				}
				if in.takes[short] || in.version[short] {
					break
				}
			}
		} else if len(flag) > 2 {
			for long, why := range in.refuse {
				if strings.HasPrefix(long, flag) {
					return fmt.Errorf("%s %s", a, why)
				}
			}
		}
		if !inline && (in.takes[flag] || in.version[flag]) {
			if i+1 >= len(args) {
				return fmt.Errorf("%s names no value", flag)
			}
			i++
			value = args[i]
		}
		if in.version[flag] {
			// cargo reads a bare MAJOR.MINOR.PATCH here as that version
			// exactly — unlike a dependency, it is not a caret requirement.
			if !in.exact(value) {
				return fmt.Errorf("%s %s is not one exact version; write %s", flag, value, in.example)
			}
			flagVersion = value
		}
	}
	if len(pkgs) == 0 {
		return fmt.Errorf("%s names no package, so what it installs is decided outside this line", in.name())
	}
	for _, p := range pkgs {
		name, v, ok := in.pin(p)
		if !ok {
			return fmt.Errorf("%s is not a package %s can be pinned by; write %s", p, in.name(), in.example)
		}
		if v == "" && flagVersion != "" && len(pkgs) == 1 {
			v = flagVersion
		}
		if v == "" {
			return fmt.Errorf("%s is not pinned: name one exact version, as %s", name, in.example)
		}
		if !in.exact(v) {
			return fmt.Errorf("%s at %q is not one exact version and would move under the same line: write %s", name, v, in.example)
		}
	}
	return nil
}

func installerNames() string {
	seen := map[string]bool{}
	var names []string
	for _, in := range installers {
		if n := in.lead[0]; !seen[n] {
			seen[n] = true
			names = append(names, n)
		}
	}
	return strings.Join(names, ", ")
}

func set(words ...string) map[string]bool {
	out := make(map[string]bool, len(words))
	for _, w := range words {
		out[w] = true
	}
	return out
}

// The version grammars. Each is one version and nothing a resolver would
// widen: Go reads `@v1` as the newest v1.x.y and a bare commit prefix as the
// commit, npm reads `^1.2.3` and `latest` as ranges and tags, pip reads
// `==1.*` as a range.
var (
	goVersion    = regexp.MustCompile(`^(v\d+\.\d+\.\d+(-[0-9A-Za-z.-]+)?(\+[0-9A-Za-z.-]+)?|[0-9a-f]{12,40})$`)
	semver       = regexp.MustCompile(`^v?\d+\.\d+\.\d+(-[0-9A-Za-z.-]+)?(\+[0-9A-Za-z.-]+)?$`)
	cargoVersion = regexp.MustCompile(`^=?\d+\.\d+\.\d+(-[0-9A-Za-z.-]+)?(\+[0-9A-Za-z.-]+)?$`)
	apkVersion   = regexp.MustCompile(`^[0-9A-Za-z._+-]+$`)
	pipVersion   = regexp.MustCompile(`^[0-9A-Za-z._+!-]+$`)

	goModule  = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._~/-]*$`)
	apkName   = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._+-]*$`)
	pipName   = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*(\[[A-Za-z0-9,._-]+\])?$`)
	npmName   = regexp.MustCompile(`^(@[a-z0-9~-][a-z0-9._~-]*/)?[a-z0-9~-][a-z0-9._~-]*$`)
	cargoName = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)
)

// splitAt reads name<sep>version, with the name checked against its
// installer's grammar; an argument with no separator is a name alone.
func splitAt(sep string, last bool, name *regexp.Regexp) func(string) (string, string, bool) {
	return func(arg string) (string, string, bool) {
		i := strings.Index(arg, sep)
		if last {
			i = strings.LastIndex(arg, sep)
		}
		if i <= 0 {
			return arg, "", name.MatchString(arg)
		}
		n, v := arg[:i], arg[i+len(sep):]
		return n, v, name.MatchString(n) && v != ""
	}
}

// apkPin reads apk's own constraint syntax: `=` is exact, and `~`, `<` and
// `>` are ranges, so a name carrying one of those is not a pin even though
// it has a version in it.
func apkPin(arg string) (string, string, bool) {
	i := strings.IndexAny(arg, "=<>~")
	if i < 0 {
		return arg, "", apkName.MatchString(arg)
	}
	n, op, v := arg[:i], arg[i:i+1], arg[i+1:]
	if !apkName.MatchString(n) {
		return n, "", false
	}
	if op != "=" || strings.ContainsAny(v, "=<>~") {
		return n, arg[i:], true
	}
	return n, v, true
}

// pipPin reads `name==version`. Any other comparison — `>=`, `~=`, `!=`,
// a list of them — is a range, and is handed back as the version so the
// refusal quotes it.
func pipPin(arg string) (string, string, bool) {
	i := strings.IndexAny(arg, "=<>!~,")
	if i < 0 {
		return arg, "", pipName.MatchString(arg)
	}
	n, rest := arg[:i], arg[i:]
	if !pipName.MatchString(n) {
		return n, "", false
	}
	if v, ok := strings.CutPrefix(rest, "=="); ok && !strings.HasPrefix(v, "=") {
		return n, v, true
	}
	return n, rest, true
}

var (
	pipRefuse = map[string]string{
		"-r":            "installs what a requirements file says, and that file is not this line",
		"--requirement": "installs what a requirements file says, and that file is not this line",
		"-c":            "reads its versions from a constraints file, which is not this line",
		"--constraint":  "reads its versions from a constraints file, which is not this line",
		"-e":            "installs a working tree, whose contents are not this line",
		"--editable":    "installs a working tree, whose contents are not this line",
		"-U":            "asks for the newest version, which moves",
		"--upgrade":     "asks for the newest version, which moves",
		"--pre":         "widens what a version may resolve to",
	}
	pipTakes = set("-i", "--index-url", "--extra-index-url", "-f", "--find-links",
		"-t", "--target", "--prefix", "--root", "--platform", "--python-version",
		"--implementation", "--abi", "--only-binary", "--no-binary", "--python",
		"--progress-bar", "--log", "--cache-dir", "--src", "--trusted-host")
	goTakes = set("-C", "-p", "-asmflags", "-buildmode", "-compiler", "-gccgoflags",
		"-gcflags", "-installsuffix", "-ldflags", "-mod", "-modfile", "-overlay",
		"-pgo", "-pkgdir", "-tags", "-toolexec", "-o")
	npmTakes   = set("--prefix", "--registry", "--cache", "--userconfig", "-C", "--dir")
	npmRefuse  = map[string]string{"--tag": "installs whatever a dist-tag points at, which moves"}
	apkTakes   = set("-X", "--repository", "-t", "--virtual", "-p", "--root", "--arch", "--cache-dir", "--keys-dir", "--repositories-file")
	cargoTakes = set("--root", "--target", "-F", "--features", "--bin", "--example",
		"--profile", "-j", "--jobs", "--index", "--registry", "--target-dir", "--config", "-Z")
	cargoRefuse = map[string]string{
		"--git":    "installs from a repository's branch, which moves",
		"--branch": "installs from a branch, which moves",
		"--path":   "installs a working tree, whose contents are not this line",
	}
)

// installers are the commands a pin can be read from, most specific lead
// first so `python3 -m pip install` is not read as something shorter.
var installers = func() []installer {
	goIn := installer{
		takes: goTakes, pin: splitAt("@", true, goModule), exact: goVersion.MatchString,
		example: "module/cmd/tool@v1.2.3",
		refuse:  map[string]string{},
	}
	pip := installer{
		takes: pipTakes, refuse: pipRefuse, pin: pipPin, exact: pipVersion.MatchString,
		example: "package==1.2.3",
	}
	npm := installer{
		takes: npmTakes, refuse: npmRefuse, pin: splitAt("@", true, npmName), exact: semver.MatchString,
		example: "package@1.2.3",
	}
	apk := installer{
		takes: apkTakes, refuse: map[string]string{"-u": "asks for the newest version, which moves", "--upgrade": "asks for the newest version, which moves"},
		pin: apkPin, exact: apkVersion.MatchString, example: "package=1.2.3-r0",
	}
	cargo := installer{
		takes: cargoTakes, refuse: cargoRefuse, version: set("--version", "--vers"),
		pin: splitAt("@", false, cargoName), exact: cargoVersion.MatchString,
		example: "crate@1.2.3",
	}
	with := func(in installer, lead ...string) installer {
		in.lead = lead
		return in
	}
	return []installer{
		with(pip, "python3", "-m", "pip", "install"),
		with(pip, "python", "-m", "pip", "install"),
		with(pip, "pip3", "install"),
		with(pip, "pip", "install"),
		with(pip, "pipx", "install"),
		with(goIn, "go", "install"),
		with(npm, "npm", "install"),
		with(npm, "npm", "i"),
		with(npm, "npm", "add"),
		with(npm, "pnpm", "add"),
		with(apk, "apk", "add"),
		with(cargo, "cargo", "install"),
	}
}()
