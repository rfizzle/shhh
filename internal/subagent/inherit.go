package subagent

// What a child may be handed of the conversation that spawned it: the last
// few turns, ahead of its task, when the spawn asks for them.
//
// A child is task-only by default, and the notebook is how the rest of what
// the session knows reaches it. Inheriting is the bounded exception for a
// subtask of the work in hand, where a briefing written from the turns would
// be the parent's conclusions and the child needs what the parent read.
// See docs/capabilities/subagents.md#what-they-share.

import (
	"fmt"
	"strings"

	"github.com/rfizzle/shhh/internal/agent"
	"github.com/rfizzle/shhh/internal/digest"
	"github.com/rfizzle/shhh/internal/provider"
)

// SetConversation names where the session's own conversation is read from
// when one of its direct children asks to inherit turns. The session is not
// an agent this package holds, so the front-end that owns it says where its
// messages are; nil, the default, hands a child of the session nothing.
//
// It is called from the spawn's own goroutine, so what it returns must be
// safe to read there: a surface that runs the spawn on the goroutine that
// appends to its conversation can hand over the agent's own Messages, and one
// that runs it elsewhere hands over a copy taken before it dispatched.
//
// It is asked at the spawn and never again: what a child inherits is the
// turns as they stood when it was spawned, and a retry re-issues that same
// text rather than reading a conversation that has since moved on.
func (s *Supervisor) SetConversation(messages func() []provider.Message) {
	s.mu.Lock()
	s.conversation = messages
	s.mu.Unlock()
}

// conversationOf is the conversation of the agent asking for a spawn: the
// session's for "", and the calling child's own otherwise, since a child that
// delegates is the parent of what it spawns.
//
// A child's copy is taken while it is inside its own tool round — the spawn
// is one of its calls, and only its own loop appends to its conversation,
// between rounds. The session's is whatever SetConversation handed over.
func (s *Supervisor) conversationOf(caller string) []provider.Message {
	if caller == "" {
		s.mu.Lock()
		read := s.conversation
		s.mu.Unlock()
		if read == nil {
			return nil
		}
		return append([]provider.Message(nil), read()...)
	}
	c, err := s.lookup(caller)
	if err != nil {
		return nil
	}
	c.mu.Lock()
	a := c.agent
	c.mu.Unlock()
	if a == nil {
		return nil
	}
	return append([]provider.Message(nil), a.Messages()...)
}

// lastTurns is the tail of msgs holding its last n turns, and how many turns
// that is — fewer than n where the conversation is shorter. A turn opens at a
// user message somebody wrote: a message the session wrote itself (a steer, a
// check-in, a gate verdict) is part of the turn it arrived in, not the start
// of another, so counting turns the way the transcript does. The system
// prompt is never part of it; the child has its own.
func lastTurns(msgs []provider.Message, n int) ([]provider.Message, int) {
	if n <= 0 {
		return nil, 0
	}
	start, got := -1, 0
	for i := len(msgs) - 1; i >= 0 && got < n; i-- {
		if msgs[i].Role == provider.RoleUser && !msgs[i].Machine {
			start, got = i, got+1
		}
	}
	if start < 0 {
		return nil, 0
	}
	var out []provider.Message
	for _, m := range msgs[start:] {
		if m.Role != provider.RoleSystem {
			out = append(out, m)
		}
	}
	return out, got
}

// inheritedPrologue renders inherited turns as the text the child's first
// turn opens with. The person's words and the assistant's are carried whole;
// a tool result is replaced the way the window trim replaces one, with the
// same recoverable placeholder, because a result is the part of a turn that
// is both the largest and the easiest to read again — and the placeholder
// names the id that reads it back. Every message goes through scrub first, so
// a secret the parent's conversation held never reaches the child's.
//
// archive is where an elided result is kept; nil elides it for good.
func inheritedPrologue(turns []provider.Message, count int, scrub func(provider.Message) provider.Message,
	archive func(tool, content string) (string, bool)) string {
	if count <= 0 || len(turns) == 0 {
		return ""
	}
	called := map[string]string{}
	var sb strings.Builder
	fmt.Fprintf(&sb, "What follows is the %s of the conversation that spawned you — your parent's, not yours. "+
		"Nothing before it was given to you. Tool results in it are elided; where a placeholder names an id, the original can be read back.\n\n",
		lastTurnsPhrase(count))
	for _, m := range turns {
		if scrub != nil {
			m = scrub(m)
		}
		switch m.Role {
		case provider.RoleUser:
			who := "user"
			if m.Machine {
				who = "session note"
			}
			sb.WriteString(who + ": " + strings.TrimSpace(m.Content) + "\n\n")
		case provider.RoleAssistant:
			if text := strings.TrimSpace(m.Content); text != "" {
				sb.WriteString("assistant: " + text + "\n\n")
			}
			for _, tc := range m.ToolCalls {
				called[tc.ID] = tc.Name
				line := "assistant called " + tc.Name
				if target := digest.Arg(tc.Name, tc.Arguments); target != "" {
					line += " · " + target
				}
				sb.WriteString(line + "\n\n")
			}
		case provider.RoleTool:
			name := called[m.ToolCallID]
			sb.WriteString("tool result")
			if name != "" {
				sb.WriteString(" (" + name + ")")
			}
			sb.WriteString(": " + agent.ElideResult(name, m.Content, archive) + "\n\n")
		}
	}
	sb.WriteString("That is the end of your parent's turns. Your own task follows.\n\n")
	return sb.String()
}

// measuringArchive stands in for the store while a spawn is being admitted.
// The placeholder a kept result leaves names an id of a fixed length, so the
// text built with this one measures what the real one will — and nothing is
// written to the store for a spawn the floor goes on to refuse.
func measuringArchive(archive func(tool, content string) (string, bool)) func(tool, content string) (string, bool) {
	if archive == nil {
		return nil
	}
	return func(string, string) (string, bool) { return "ev-0000000000000000", true }
}

// inheritedClause is how a refusal names the inherited turns among what it
// counted, and nothing where there are none.
func inheritedClause(count int, tokens int64) string {
	if count <= 0 {
		return ""
	}
	return fmt.Sprintf(", your %s (~%s tokens)", lastTurnsPhrase(count), formatTokens(tokens))
}

// lastTurnsPhrase is how many turns a child was handed, as every sentence
// about it says it: "last turn", "last 3 turns".
func lastTurnsPhrase(count int) string {
	if count == 1 {
		return "last turn"
	}
	return fmt.Sprintf("last %d turns", count)
}
