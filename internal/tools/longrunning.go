package tools

import (
	"path"
	"strings"
)

// LongRunningError is what a command of a shape that cannot finish is answered
// with in place of a run: the model reads it as the call's result and starts
// the same line as a process.
//
// A server or a watcher run as a command sits until the session's ceiling
// hands it to the process supervisor, ten minutes on, with nothing read from
// it meanwhile. The redirect is a closed list of shapes that fail open: a
// long-running command nobody listed still runs as it always did, and the
// ceiling's hand-over row teaches the rest. The person's own /run and !
// never come through the model's tool, so they are not asked.
// See docs/capabilities/containment.md#a-command-that-will-not-finish-is-not-waited-on-forever.
type LongRunningError struct{}

func (LongRunningError) Error() string {
	return "this looks long-running: start it with the process tool (action start) and read it with action read"
}

// LongRunning reports whether command, as the model wrote it, has a shape
// that cannot finish on its own. Each segment of a compound line is read by
// its leading command; a segment sent to the background with a trailing & is
// already not waited on.
func LongRunning(command string) bool {
	for _, seg := range splitSegments(command) {
		if !seg.background && longRunningWords(seg.words) {
			return true
		}
	}
	return false
}

type segment struct {
	words      []string
	background bool
}

// splitSegments breaks a command line at the unquoted separators && || ; | &
// and newlines, and each piece into words. It reads quotes and backslashes
// only far enough that a separator inside a string does not split it.
func splitSegments(line string) []segment {
	var segs []segment
	var words []string
	var word strings.Builder
	inWord := false
	endWord := func() {
		if inWord {
			words = append(words, word.String())
			word.Reset()
			inWord = false
		}
	}
	endSeg := func(background bool) {
		endWord()
		if len(words) > 0 {
			segs = append(segs, segment{words: words, background: background})
		}
		words = nil
	}
	var quote byte
	for i := 0; i < len(line); i++ {
		c := line[i]
		switch {
		case quote != 0:
			if c == quote {
				quote = 0
			} else if c == '\\' && quote == '"' && i+1 < len(line) {
				i++
				word.WriteByte(line[i])
			} else {
				word.WriteByte(c)
			}
		case c == '\'' || c == '"':
			quote = c
			inWord = true
		case c == '\\' && i+1 < len(line):
			i++
			word.WriteByte(line[i])
			inWord = true
		case c == ' ' || c == '\t':
			endWord()
		case c == '\n' || c == ';' || c == '|':
			endSeg(false)
		case c == '&':
			switch {
			case i+1 < len(line) && line[i+1] == '&':
				i++
				endSeg(false)
			case i+1 < len(line) && line[i+1] == '>', i > 0 && (line[i-1] == '>' || line[i-1] == '<'):
				// 2>&1, &>file: a redirection, part of the word.
				word.WriteByte(c)
				inWord = true
			default:
				endSeg(true)
			}
		default:
			word.WriteByte(c)
			inWord = true
		}
	}
	endSeg(false)
	return segs
}

// longRunningWords reads one segment's leading command against the list.
func longRunningWords(words []string) bool {
	words = stripWrappers(words)
	if len(words) == 0 {
		return false
	}
	cmd, args := path.Base(words[0]), words[1:]
	switch cmd {
	case "npm", "pnpm", "yarn", "bun":
		return packageServer(args)
	case "vite":
		return len(args) == 0 || first(args) == "dev" || first(args) == "serve" || buildWatch(cmd, args)
	case "next":
		return first(args) == "dev"
	case "rails":
		return first(args) == "s" || first(args) == "server"
	case "flask":
		return has(args, "run")
	case "uvicorn", "nodemon", "air", "watch":
		return true
	case "python", "python3":
		if first(args) == "-m" && len(args) > 1 {
			return longRunningWords(append([]string{args[1]}, args[2:]...))
		}
	case "http.server":
		return true
	case "go":
		return goRunsAServer(args)
	case "tail":
		return tailFollows(args)
	case "cargo":
		return first(args) == "watch"
	case "docker", "docker-compose":
		return composeUpAttached(cmd, args)
	}
	return buildWatch(cmd, args)
}

// stripWrappers drops what stands before the command that matters: variable
// assignments, exec and time, and the package runners npx and bunx with their
// own flags.
func stripWrappers(words []string) []string {
	for len(words) > 0 {
		w := words[0]
		switch {
		case isAssignment(w), w == "exec", w == "time":
			words = words[1:]
		case w == "npx" || w == "bunx":
			words = words[1:]
			for len(words) > 0 && strings.HasPrefix(words[0], "-") {
				words = words[1:]
			}
		case (w == "pnpm" || w == "bundle") && first(words[1:]) == "exec":
			words = words[2:]
		default:
			return words
		}
	}
	return words
}

func isAssignment(w string) bool {
	i := strings.IndexByte(w, '=')
	if i <= 0 {
		return false
	}
	for _, r := range w[:i] {
		if r != '_' && (r < 'A' || r > 'Z') && (r < 'a' || r > 'z') && (r < '0' || r > '9') {
			return false
		}
	}
	return true
}

func first(args []string) string {
	if len(args) == 0 {
		return ""
	}
	return args[0]
}

func has(args []string, want string) bool {
	for _, a := range args {
		if a == want {
			return true
		}
	}
	return false
}

// serverScripts are the script names that start a development server.
var serverScripts = map[string]bool{"dev": true, "start": true, "serve": true}

// packageServer reads npm, pnpm, yarn and bun: the script run is dev, start
// or serve, with or without the word run.
func packageServer(args []string) bool {
	if first(args) == "run" || first(args) == "run-script" {
		args = args[1:]
	}
	return serverScripts[first(args)]
}

// buildTools are the commands whose --watch or -w keeps them running; -w on
// anything else (grep -w, wc -w) means something else.
var buildTools = map[string]bool{
	"tsc": true, "webpack": true, "esbuild": true, "rollup": true, "jest": true,
	"sass": true, "tailwindcss": true, "parcel": true, "vite": true,
}

func buildWatch(cmd string, args []string) bool {
	if !buildTools[cmd] {
		return false
	}
	for _, a := range args {
		if a == "-w" || a == "--watch" || a == "--watchAll" || strings.HasPrefix(a, "--watch=") {
			return true
		}
	}
	return false
}

// goRunsAServer reads go run of a package named for a server: server, serve
// or daemon, or a program under cmd/ named with a trailing d. Words that are
// not a build verb's own are the build's flags, and the package is the first
// word that is not one.
func goRunsAServer(args []string) bool {
	if first(args) != "run" {
		return false
	}
	for _, a := range args[1:] {
		if strings.HasPrefix(a, "-") {
			continue
		}
		return serverPackage(a)
	}
	return false
}

// notDaemons are programs under cmd/ that end in d without being one.
var notDaemons = map[string]bool{
	"build": true, "add": true, "load": true, "upload": true, "download": true,
	"record": true, "read": true, "send": true, "find": true, "seed": true,
	"feed": true, "embed": true, "bind": true, "command": true, "append": true,
	"sed": true, "diff": true, "hold": true, "board": true,
}

func serverPackage(pkg string) bool {
	pkg = strings.TrimSuffix(strings.TrimSuffix(pkg, "/..."), ".go")
	base := path.Base(pkg)
	switch base {
	case "server", "serve", "daemon":
		return true
	}
	underCmd := strings.Contains("/"+strings.TrimPrefix(pkg, "./"), "/cmd/")
	return underCmd && len(base) > 2 && strings.HasSuffix(base, "d") && !notDaemons[base]
}

// tailFollows reads tail's -f, -F and --follow, alone or in a short cluster.
func tailFollows(args []string) bool {
	for _, a := range args {
		switch {
		case a == "--follow" || strings.HasPrefix(a, "--follow="):
			return true
		case strings.HasPrefix(a, "-") && !strings.HasPrefix(a, "--") && strings.ContainsAny(a, "fF"):
			return true
		}
	}
	return false
}

// composeUpAttached reads docker compose up that stays attached: no -d or
// --detach among its arguments.
func composeUpAttached(cmd string, args []string) bool {
	if cmd == "docker" {
		if first(args) != "compose" {
			return false
		}
		args = args[1:]
	}
	if !has(args, "up") {
		return false
	}
	for _, a := range args {
		if a == "--detach" || a == "--wait" || (strings.HasPrefix(a, "-") && !strings.HasPrefix(a, "--") && strings.Contains(a, "d")) {
			return false
		}
	}
	return true
}
