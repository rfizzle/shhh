package secret

import (
	"strings"

	"github.com/rfizzle/shhh/internal/provider"
)

// PromptBlock describes what this session hides from the model and how it
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
func PromptBlock(v *Vault) string {
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
		b.WriteString(". One of them reading as unset is that mask and not a broken setup: name the variable you need and the user can hand it over.\n")
	}
	b.WriteString("- Text that carries a well-known credential's shape — an AWS access key, a GitHub or Slack token, a private-key block, a JWT — is replaced with `")
	b.WriteString(Redacted("kind"))
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
	b.WriteString("- You will never see a value. Anywhere one would appear — printed, echoed, encoded, read from a file, a fragment of it — it is replaced with the placeholder `" + Placeholder(names[0]) + "` before it reaches you. ")
	b.WriteString("That placeholder in output means the secret was there and was used; it is not an error and not a literal to copy.\n")
	b.WriteString("- Do not try to reveal a value (printing, base64, splitting it up). It will not work, and the user chose not to show it to you.\n")
	b.WriteString("- A command that fails with the variable unset is a sign the variable name is wrong; the names above are exact and case-sensitive.\n")
	return b.String()
}

// ScrubMessages returns msgs with every text field scrubbed, for a request
// about to leave for a provider. The slice is copied; messages are values,
// so the caller's conversation is untouched.
//
// A vault with nothing declared still walks the messages, because the pass
// over credential shapes has nothing to do with what was declared and this
// is the last door before the text leaves the machine.
func (v *Vault) ScrubMessages(msgs []provider.Message) []provider.Message {
	if v == nil {
		return msgs
	}
	out := make([]provider.Message, len(msgs))
	for i, m := range msgs {
		out[i] = v.ScrubMessage(m)
	}
	return out
}

// ScrubMessage scrubs one message's text: its content, the arguments of
// the tool calls it made, and any text attachment. Reasoning blocks are the
// provider's own signed form and are left alone; a rewritten block fails
// the next request outright, and the model wrote them without the value.
func (v *Vault) ScrubMessage(m provider.Message) provider.Message {
	if v == nil {
		return m
	}
	m.Content = v.Scrub(m.Content)
	if len(m.ToolCalls) > 0 {
		calls := make([]provider.ToolCall, len(m.ToolCalls))
		for i, tc := range m.ToolCalls {
			tc.Arguments = v.Scrub(tc.Arguments)
			calls[i] = tc
		}
		m.ToolCalls = calls
	}
	if len(m.Attachments) > 0 {
		atts := make([]provider.Attachment, len(m.Attachments))
		for i, a := range m.Attachments {
			if a.Kind == provider.AttachmentText {
				a.Data = []byte(v.Scrub(string(a.Data)))
			}
			atts[i] = a
		}
		m.Attachments = atts
	}
	return m
}
