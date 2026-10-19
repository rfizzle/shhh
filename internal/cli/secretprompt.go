package cli

import (
	"strings"

	"github.com/rfizzle/agentcore/secret"
)

// secretPromptBlock describes what this session hides from the model and how it
// will notice: the declared secrets by name, the mask over the variables
// nobody declared, and the two placeholders that stand where a value would
// have been. Empty when there is nothing to hide and no mask, so the
// section costs nothing until it is needed.
//
// The block says how a hidden value shows up rather than only that it is
// hidden, because a model that does not know the placeholder reads it as
// the command having failed and starts debugging the wrong thing — and the
// same is true of the mask, where the symptom is a variable that is simply
// unset and looks exactly like a machine that was never configured.
//
// asks is whether a person can answer /secret during the session. The mask's
// way out is a declaration and never the value typed into the conversation,
// which would put the credential exactly where the vault exists to keep it
// out; a -p run, a served session and a sub-agent cannot be answered, so they
// name the variable in what they report instead.
// See docs/capabilities/secrets.md#a-masked-variable-is-asked-for-by-name.
func secretPromptBlock(v *secret.Vault, asks bool) string {
	names := v.Names()
	masked := v.EnvMask()
	if len(names) == 0 && !masked {
		return ""
	}
	var b strings.Builder
	b.WriteString("## Secrets\n\n")
	if len(names) > 0 {
		b.WriteString(namedSecrets(names))
	} else {
		b.WriteString("This session keeps credentials out of what you run and what you read; the user has declared none of them to you by name.\n\n")
	}
	if masked {
		b.WriteString("- Variables whose names end in `_KEY`, `_SECRET` or `_TOKEN` are removed from the environment of every command you run")
		if len(names) > 0 {
			b.WriteString(", unless the user declared one as a secret above")
		}
		b.WriteString(". One of them reading as unset is that mask and not a broken setup. ")
		if asks {
			b.WriteString("Name the variable you need and ask the user to declare it with `/secret set NAME`, which hands a command the value without showing it to you. ")
		} else {
			b.WriteString("Nobody can declare one from inside this run: name the variable you need in what you report, so it can be declared before the work is run again. ")
		}
		b.WriteString("Never ask for the value itself or suggest pasting it into the conversation — you would read it.\n")
	}
	b.WriteString("- Text that carries a well-known credential's shape — an AWS access key, a GitHub or Slack token, a private-key block, a JWT — is replaced with `")
	b.WriteString(secret.Redacted("kind"))
	b.WriteString("` wherever you would have read it, whoever it belongs to. That is a value that was there, not an error and not a literal to copy.\n")
	return b.String()
}

// namedSecrets is the part of the block that exists only when the user
// declared something: the names, the one way to reach them, and the
// placeholder that names the secret back.
func namedSecrets(names []string) string {
	var b strings.Builder
	b.WriteString("This session holds secret values the user set aside for you to use without seeing. ")
	b.WriteString("Each is an environment variable in every command you run and every script a command runs: ")
	for i, n := range names {
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteString("`$" + n + "`")
	}
	b.WriteString(".\n\n")
	b.WriteString("- Reference a secret by its variable, never by value: `curl -H \"Authorization: Bearer $" + names[0] + "\"`, ")
	b.WriteString("`os.environ[\"" + names[0] + "\"]`, `process.env." + names[0] + "`. Scripts you write and then run with a command get the variable too.\n")
	b.WriteString("- You will never see a value. Anywhere one would appear — printed, echoed, encoded, read from a file, a fragment of it — it is replaced with the placeholder `" + secret.Placeholder(names[0]) + "` before it reaches you. ")
	b.WriteString("That placeholder in output means the secret was there and was used; it is not an error and not a literal to copy.\n")
	b.WriteString("- Do not try to reveal a value (printing, base64, splitting it up). It will not work, and the user chose not to show it to you.\n")
	b.WriteString("- A command that fails with the variable unset is a sign the variable name is wrong; the names above are exact and case-sensitive.\n")
	return b.String()
}
