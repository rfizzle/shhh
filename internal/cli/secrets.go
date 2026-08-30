package cli

// Session secrets: values a command may use and the model may never see.
// The CLI owns the vault and every place a value has to be put or taken
// out — the command environment, the process supervisor, the executor
// chain, the request stream, and /secret. See docs/capabilities/secrets.md.

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/rfizzle/shhh/internal/config"
	"github.com/rfizzle/shhh/internal/secret"
	"github.com/rfizzle/shhh/internal/ui/chat"
)

// loadSecrets builds the session vault from config's secrets.env and the
// --secret flags. A configured name that is unset is a warning on warn and
// not a secret: config is standing policy, and a laptop without the
// variable set should still open a session. A flag naming an unset
// variable is an error: the user asked for it here and now.
func loadSecrets(cfg config.Config, flags []string, warn io.Writer) (*secret.Vault, error) {
	v := secret.New()
	for _, name := range cfg.Secrets.Env {
		val, ok := os.LookupEnv(name)
		if !ok || val == "" {
			fmt.Fprintf(warn, "warning: secrets.env names %s, which is not set; skipped\n", name)
			continue
		}
		if err := v.Add(name, val); err != nil {
			return nil, fmt.Errorf("config secrets.env: %w", err)
		}
	}
	for _, spec := range flags {
		name, val, err := secretFromSpec(spec)
		if err != nil {
			return nil, fmt.Errorf("--secret %s: %w", spec, err)
		}
		if err := v.Add(name, val); err != nil {
			return nil, fmt.Errorf("--secret: %w", err)
		}
	}
	return v, nil
}

// secretFromSpec resolves one NAME or NAME=value spec. A bare name reads
// the value from the environment, which is the form to prefer: a value on
// the command line is in the shell's history and every process listing.
func secretFromSpec(spec string) (name, value string, err error) {
	if name, value, found := strings.Cut(spec, "="); found {
		return name, value, nil
	}
	val, ok := os.LookupEnv(spec)
	if !ok || val == "" {
		return "", "", fmt.Errorf("environment variable %s is not set", spec)
	}
	return spec, val, nil
}

// secretsManager backs the /secret slash command. Beyond the note for the
// screen it returns what the model should be told, if anything: a secret
// added mid-session is unusable until the model knows its name, and the
// system prompt was written before it existed.
func secretsManager(v *secret.Vault) func(args []string) (note, announce string) {
	const usage = "Usage: /secret [list] · /secret set NAME (from the environment) · /secret set NAME=value · /secret forget NAME"
	return func(args []string) (string, string) {
		if len(args) == 0 || (len(args) == 1 && args[0] == "list") {
			return secretsListing(v), ""
		}
		switch args[0] {
		case "set", "add":
			if len(args) != 2 {
				return usage, ""
			}
			name, val, err := secretFromSpec(args[1])
			if err != nil {
				return "Error: " + err.Error(), ""
			}
			if err := v.Add(name, val); err != nil {
				return "Error: " + err.Error(), ""
			}
			return fmt.Sprintf("Secret %s set (%d bytes). Commands see it as $%s; the model sees %s.",
					name, len(val), name, secret.Placeholder(name)),
				fmt.Sprintf("A secret named %s is now available to every command you run as the environment variable $%s. "+
					"Use the variable, never a value; anywhere the value would appear you will see %s instead.",
					name, name, secret.Placeholder(name))
		case "forget", "unset", "rm":
			if len(args) != 2 {
				return usage, ""
			}
			if !v.Remove(args[1]) {
				return "No secret named " + args[1] + ".", ""
			}
			return "Forgot secret " + args[1] + ". Commands no longer see $" + args[1] + ".",
				"The secret " + args[1] + " has been removed; $" + args[1] + " is no longer set for commands."
		}
		return usage, ""
	}
}

// secretsListing is the /secret list text: names only, never lengths or
// prefixes that would narrow a guess.
func secretsListing(v *secret.Vault) string {
	names := v.Names()
	if len(names) == 0 {
		return "No secrets in this session. /secret set NAME reads one from the environment; --secret NAME or secrets.env in config sets them at start."
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%d secret(s), each an environment variable in every command the model runs, masked as [secret:NAME] in everything it reads:\n", len(names))
	for _, n := range names {
		b.WriteString("  $" + n + "\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

// scrubRunner scrubs a command runner's output. The runner's output is
// also the transcript row and the live tail on screen, which is why the
// scrub is here and not only on the tool result: a value on the screen is
// in the scrollback, the copy buffer and the recording.
func scrubRunner(v *secret.Vault, run func(context.Context, string) (string, int)) func(context.Context, string) (string, int) {
	if run == nil || v == nil {
		return run
	}
	return func(ctx context.Context, command string) (string, int) {
		out, code := run(ctx, command)
		return v.Scrub(out), code
	}
}

// scrubTailRunner is scrubRunner for the tailed form: each live line is
// scrubbed on its own, so a value split across lines is caught by the
// whole-output scrub at the end rather than the line one.
func scrubTailRunner(v *secret.Vault, run func(context.Context, string, func(string)) (string, int)) func(context.Context, string, func(string)) (string, int) {
	if run == nil || v == nil {
		return run
	}
	return func(ctx context.Context, command string, onLine func(string)) (string, int) {
		if onLine != nil {
			inner := onLine
			onLine = func(line string) { inner(v.Scrub(line)) }
		}
		out, code := run(ctx, command, onLine)
		return v.Scrub(out), code
	}
}

// scrubContainment wraps a containment's runners, which replace the plain
// ones when a mechanism is available.
func scrubContainment(v *secret.Vault, c chat.Containment) chat.Containment {
	c.Run = scrubRunner(v, c.Run)
	c.TailRun = scrubTailRunner(v, c.TailRun)
	return c
}
