package cli

// The checkout's toolchain declaration on the host path: which of the
// binaries it names the contained PATH lacks, and the install the person is
// offered for them. The declaration's loader is internal/project; this is
// where a session reads it, says what is missing, and builds the one way its
// install lines run.
// See docs/capabilities/containment.md#a-checkout-declares-the-toolchain-its-work-needs.

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"

	"github.com/rfizzle/shhh/internal/config"
	"github.com/rfizzle/shhh/internal/project"
	"github.com/rfizzle/shhh/internal/runner"
	"github.com/rfizzle/shhh/internal/sandbox"
	"github.com/rfizzle/shhh/internal/shell"
	"github.com/rfizzle/shhh/internal/tools"
	"github.com/rfizzle/shhh/internal/ui/chat"
	"github.com/rfizzle/shhh/internal/ui/components"
)

// toolchainReading is the declaration as a session found it.
type toolchainReading struct {
	tc project.Toolchain
	// declared is a trusted checkout with a declaration that loaded.
	declared bool
	// err is a declaration that is there, trusted, and does not load.
	err error
	// dir is shhh's own toolchain directory and bin the directory under it
	// every captured command's PATH ends with. Both are empty where the
	// machine has no cache directory to put one in.
	dir, bin string
	// missing is what the check list names and that PATH does not have.
	missing []string
}

// openToolchain reads the checkout's declaration and, where there is one,
// puts shhh's own toolchain directory on the end of every captured command's
// PATH — so a tool an earlier session installed is found, and so what is
// missing is judged against the PATH a contained command is actually handed.
// Reading it again is cheap (a stat, a small file, a PATH walk), which is why
// each of the three places that need it asks rather than being handed one.
func openToolchain() toolchainReading {
	tc, ok, err := project.LoadToolchain(projectTrust())
	r := toolchainReading{tc: tc, declared: ok, err: err}
	if !ok {
		return r
	}
	if dir, err := sandbox.ToolchainDir(); err == nil {
		r.dir, r.bin = dir, filepath.Join(dir, "bin")
		runner.SetPathAfter(r.bin)
	}
	r.missing = tc.Missing(runner.PathValue())
	return r
}

// toolchainStartupNote is the line a session prints before it starts when the
// declaration names tools the PATH lacks, beside the trust note and for the
// same reason: a headless run has no screen to read it off later. It says the
// install is a session's to offer, because a run with nobody to ask is the
// one place it is not offered at all.
func toolchainStartupNote(r toolchainReading) string {
	if r.err != nil {
		return "toolchain: " + r.err.Error()
	}
	if len(r.missing) == 0 {
		return ""
	}
	return "toolchain: this checkout declares " + joinAnd(r.missing) +
		", not on PATH — /setup in a session installs them; a run with nobody to ask does not"
}

// toolchainPromptBlock tells the model which declared binaries are missing,
// and nothing at all where none is: a paragraph about tools that are all
// there would be one more thing to read on every request for no reason.
// offered is a session whose person has been offered the install; a run with
// nobody to offer it to says so instead, so the model does not ask a person
// who is not there.
// See docs/capabilities/containment.md#a-checkout-declares-the-toolchain-its-work-needs.
func toolchainPromptBlock(missing []string, offered bool) string {
	if len(missing) == 0 {
		return ""
	}
	them := "them"
	if len(missing) == 1 {
		them = "it"
	}
	b := "# Missing tools\nThis checkout's toolchain declaration names " + joinAnd(missing) +
		", which the PATH your commands run with does not have. "
	if offered {
		b += "The user has been offered an install of " + them + ". Do not install " + them +
			" yourself or fetch " + them + " another way: where your work needs one, say which and ask the user to install it."
	} else {
		b += "Nobody here can install " + them + ", and you must not install " + them +
			" yourself: where your work needs one, say which, and do what you can without it."
	}
	return b
}

// toolchainCard is the declaration as the chat session holds it, with the
// install behind its card. avail and refusal are the containment the
// session's own commands run under, because the install runs under exactly
// that: contained where a mechanism is, refused where one is required and
// none is, and bare otherwise — as a hook is, since a card the person
// answered is no stronger a reason to run bare than the command card beside
// it.
func toolchainCard(r toolchainReading, cfg config.Config, avail sandbox.Availability, refusal string) chat.Toolchain {
	if !r.declared {
		return chat.Toolchain{}
	}
	t := chat.Toolchain{
		Declared: r.tc.Check,
		Missing:  r.missing,
		Lines:    r.tc.HostInstall(),
		Bin:      shortPath(r.bin),
		Refusal:  refusal,
		Recheck:  func() []string { return openToolchain().missing },
	}
	if r.bin == "" {
		// Nowhere of shhh's own to install into, so nothing is offered:
		// the alternative is a directory on the person's own PATH.
		return t
	}
	if p, err := installPolicy(cfg, r); err == nil && avail.OK {
		t.Network = p.Profile != sandbox.ProfileWorkspaceNetless
		if t.Network && sandbox.HoldsHosts(avail.Mechanism) {
			t.Hosts = p.AllowHosts
		}
	}
	t.Install = func(ctx context.Context, line string) tools.ExecResult {
		return installLine(ctx, r, cfg, avail, refusal, line)
	}
	return t
}

// installLine runs one line of the declaration the way the card's yes runs
// it. It is the only thing that runs one: nothing puts a line to the
// classifier or to a mode, and the chat session is the only caller.
func installLine(ctx context.Context, r toolchainReading, cfg config.Config, avail sandbox.Availability, refusal, line string) tools.ExecResult {
	if refusal != "" {
		return runner.WrapFailure(r.dir, errors.New(refusal))
	}
	if err := os.MkdirAll(r.bin, 0o755); err != nil {
		return runner.WrapFailure(r.dir, err)
	}
	argv, err := installArgv(r, cfg, avail, line)
	if err != nil {
		return runner.WrapFailure(r.dir, err)
	}
	result := runner.RunCaptureArgvInResult(ctx, r.dir, line, argv)
	if avail.OK {
		return readContained(avail.Mechanism, result)
	}
	return result
}

// installArgv is what one line runs as: under the mechanism, the install
// policy's wrap; with none, the line with the installers' variables in front
// of it, which is the only way a bare spawn is handed them.
func installArgv(r toolchainReading, cfg config.Config, avail sandbox.Availability, line string) ([]string, error) {
	if !avail.OK {
		// The installers' variables ride an `env` in front of the line,
		// which Windows does not have; saying so beats a row that reads as
		// a line that did not start.
		if runtime.GOOS == "windows" {
			return nil, errors.New("installing a declared toolchain is not supported on Windows yet")
		}
		argv := append([]string{"env"}, installEnv(r)...)
		return append(argv, shell.Execution().Argv(line)...), nil
	}
	p, err := installPolicy(cfg, r)
	if err != nil {
		return nil, err
	}
	return sandbox.Wrap(avail, p, line)
}

// installPolicy is the session's containment narrowed for an install. The
// workspace is read-only and the scope's extra directories are left out, so
// what the lines can write is the toolchain caches the policy always grants —
// shhh's own directory among them — plus whatever sandbox.write_extra names,
// which is the person's standing answer about caches, and nothing the person
// granted the work in this session.
// The lines start in shhh's own directory, so an installer that writes where
// it stands writes there. And the declaration's hosts are the host list,
// where it names any: a registry is what an install needs, and the card
// names them before anything runs.
// See docs/capabilities/containment.md#a-contained-commands-network-can-be-a-list-of-hosts.
func installPolicy(cfg config.Config, r toolchainReading) (sandbox.Policy, error) {
	p, err := sandboxPolicy(cfg)
	if err != nil {
		return sandbox.Policy{}, err
	}
	p.Cwd = r.dir
	p.ReadOnlyWorkspace = true
	// The session's secrets are for the work, not for a line a checkout
	// wrote: with no names declared, the allowlist drops the vault's pairs
	// from the environment the line is handed.
	p.SecretNames = nil
	if len(r.tc.Hosts) > 0 {
		p.AllowHosts = slices.Clone(r.tc.Hosts)
	}
	return p.WithEnv(installEnv(r)), nil
}

// installEnv points each installer the declaration grammar knows at shhh's
// own directory rather than at the one it would choose — ~/go/bin, a global
// npm prefix, ~/.cargo/bin — which are on the person's own PATH, where a tool
// a checkout declared would shadow one they installed.
func installEnv(r toolchainReading) []string {
	return []string{
		"GOBIN=" + r.bin,
		"CARGO_INSTALL_ROOT=" + r.dir,
		"NPM_CONFIG_PREFIX=" + r.dir,
		"NPM_CONFIG_GLOBAL=true",
		"PNPM_HOME=" + r.bin,
		"PIPX_HOME=" + filepath.Join(r.dir, "pipx"),
		"PIPX_BIN_DIR=" + r.bin,
		"PYTHONUSERBASE=" + r.dir,
		"PIP_USER=1",
	}
}

// probeToolchain is the doctor's reading of the declaration.
func probeToolchain(context.Context, config.Config) doctorFinding {
	t := projectTrust()
	return doctorToolchain(openToolchain(), slices.Contains(t.Withheld(), project.KindToolchain))
}

// doctorToolchain is that reading: nothing declared is nothing to check, a
// withheld declaration is the trust row's to explain, a broken one fails, and
// a missing tool is a warning naming the one way it is installed.
func doctorToolchain(r toolchainReading, withheld bool) doctorFinding {
	switch {
	case withheld:
		return doctorFinding{Subject: "withheld", Detail: project.ToolchainFile,
			Outcome: "untrusted", State: components.DoctorSkipped}
	case r.err != nil:
		return doctorFinding{Subject: "unreadable", Detail: project.ToolchainFile, Outcome: "broken",
			State: components.DoctorFailed, Consequence: r.err.Error()}
	case !r.declared:
		return doctorFinding{Subject: "no toolchain declared", Detail: project.ToolchainFile,
			Outcome: "empty", State: components.DoctorSkipped}
	case len(r.missing) == 0:
		return doctorFinding{Subject: countOf(len(r.tc.Check), "tool", "tools") + " on PATH",
			Detail: strings.Join(r.tc.Check, " · "), Outcome: "ok"}
	}
	f := doctorFinding{
		Subject: countOf(len(r.missing), "tool", "tools") + " missing",
		Detail:  strings.Join(r.missing, " · "),
		Outcome: "missing", State: components.DoctorWarned,
		Consequence: "declared by " + project.ToolchainFile + " and not on the PATH a command is handed",
	}
	if len(r.tc.HostInstall()) > 0 && r.bin != "" {
		f.Fix = []string{"/setup   # in a session, which puts the lines on one card", "they install into " + shortPath(r.bin)}
		f.FixLabel = "show the 2 lines"
	}
	return f
}
