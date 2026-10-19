package secret

import (
	"github.com/rfizzle/llm"
	"github.com/rfizzle/llm/attachment"
)

// ScrubMessages returns msgs with every text field scrubbed, for a request
// about to leave for a provider. The slice is copied; messages are values,
// so the caller's conversation is untouched.
//
// A vault with nothing declared still walks the messages, because the pass
// over credential shapes has nothing to do with what was declared and this
// is the last door before the text leaves the machine.
func (v *Vault) ScrubMessages(msgs []llm.Message) []llm.Message {
	if v == nil {
		return msgs
	}
	out := make([]llm.Message, len(msgs))
	for i, m := range msgs {
		out[i] = v.ScrubMessage(m)
	}
	return out
}

// ScrubMessage scrubs one message's text: its content, the arguments of
// the tool calls it made, and any text attachment. Reasoning blocks are the
// provider's own signed form and are left alone; a rewritten block fails
// the next request outright, and the model wrote them without the value.
func (v *Vault) ScrubMessage(m llm.Message) llm.Message {
	if v == nil {
		return m
	}
	m.Content = v.Scrub(m.Content)
	if len(m.ToolCalls) > 0 {
		calls := make([]llm.ToolCall, len(m.ToolCalls))
		for i, tc := range m.ToolCalls {
			tc.Arguments = v.Scrub(tc.Arguments)
			calls[i] = tc
		}
		m.ToolCalls = calls
	}
	if len(m.Attachments) > 0 {
		atts := make([]attachment.Attachment, len(m.Attachments))
		for i, a := range m.Attachments {
			if a.Kind == attachment.Text {
				a.Data = []byte(v.Scrub(string(a.Data)))
			}
			atts[i] = a
		}
		m.Attachments = atts
	}
	return m
}
