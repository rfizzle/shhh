package secret

import (
	"strings"

	"github.com/rfizzle/shhh/internal/provider"
)

// PromptBlock describes the session's secrets to the model: the names, the
// one way to use them, and what it will see instead of a value. Empty when
// there are none, so the section costs nothing until it is needed.
//
// The block says how the value shows up rather than only that it is hidden,
// because a model that does not know the placeholder reads it as the
// command having failed and starts debugging the wrong thing.
func PromptBlock(v *Vault) string {
	names := v.Names()
	if len(names) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("## Secrets\n\n")
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
func (v *Vault) ScrubMessages(msgs []provider.Message) []provider.Message {
	if v == nil || v.Len() == 0 {
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
