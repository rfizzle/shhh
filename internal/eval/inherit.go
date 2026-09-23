package eval

// A child spawned with its parent's last turns, on a real model: does it act
// on what the turns held, or go and read it all again?
//
// Inheriting turns is only worth what it saves. A child handed the file its
// parent just read, and the conclusion drawn from it, that opens by reading
// the file again has cost the session the turns and the read both — and
// nothing in the repository can see that happen, because every test of the
// mechanism scripts the child's model, and a scripted model does whatever the
// script says. So this shape asks the real one, over a real supervisor, and
// compares what it did with what the row says it must: carry the fact the
// turns held into its report, and never read the files the turns had already
// read.
// See docs/capabilities/subagents.md#what-they-share.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"

	"github.com/rfizzle/shhh/internal/agent"
	"github.com/rfizzle/shhh/internal/prompt"
	"github.com/rfizzle/shhh/internal/provider"
	"github.com/rfizzle/shhh/internal/shell"
	"github.com/rfizzle/shhh/internal/subagent"
	"github.com/rfizzle/shhh/internal/tools"
)

// KindInherit spawns a reader handed the row's conversation as its parent's
// turns and asks what it did with them. It names a model: the child is one.
const KindInherit Kind = "inherit"

// What an inherit row's answer can be.
const (
	// LabelActed is a child whose report carried what the turns held and
	// which never read again what they had already read.
	LabelActed = "acted"
	// LabelReread is a child that read one of the files the turns had
	// already shown it. It is the failure this case exists for, and it
	// outranks a good report: the answer may be right and the inheritance
	// still bought nothing.
	LabelReread = "reread"
	// LabelMissed is a child whose report never carried the fact at all.
	LabelMissed = "missed"
)

// inheritChildName is the name the row's child is spawned under, so the
// report call can name it.
const inheritChildName = "child"

// askInherit runs one row's child on the real model.
func askInherit(ctx context.Context, p provider.Provider, model string, row Row) Answer {
	dir, err := os.MkdirTemp("", "shhh-eval-inherit-")
	if err != nil {
		return Answer{Row: row, Err: "cannot make a workspace: " + err.Error()}
	}
	defer func() { _ = os.RemoveAll(dir) }()
	for path, body := range row.Files {
		full := filepath.Join(dir, filepath.FromSlash(path))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			return Answer{Row: row, Err: "cannot seed the workspace: " + err.Error()}
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			return Answer{Row: row, Err: "cannot seed the workspace: " + err.Error()}
		}
	}

	child := &inheritChild{root: dir, p: p, model: model}
	ctx, cancel := context.WithCancel(ctx)
	sup := subagent.New(ctx, subagent.Options{Root: dir, NewEnv: child.factory()})
	defer sup.Close()
	defer cancel()
	sup.SetConversation(func() []provider.Message { return row.Conversation })

	exec := sup.WrapExecutor("", func(name string, _ json.RawMessage) (string, error) {
		return "", fmt.Errorf("%s is not a tool an inherit case registers", name)
	})
	if _, err := exec(subagent.SpawnToolName, scriptedArgs(map[string]any{
		"role": string(subagent.RoleResearcher), "task": row.Task, "name": inheritChildName, "inherit": row.Inherit,
	})); err != nil {
		return Answer{Row: row, Err: "the spawn was refused: " + firstLine(err.Error())}
	}
	report, err := exec(subagent.ReportToolName, scriptedArgs(map[string]any{"name": inheritChildName}))
	a := Answer{Row: row, Usage: child.used()}
	if err != nil {
		a.Err = "the report was refused: " + firstLine(err.Error())
		return a
	}
	if st, ok := sup.Get(inheritChildName); ok && st.State != subagent.StateDone {
		a.Err = "the child did not finish: " + st.Detail
		return a
	}
	if read := child.reread(row.Paths); read != "" {
		a.Label, a.Reason = LabelReread, "read "+read+" again, which the turns had already shown it"
		return a
	}
	if gone := missing(report, row.needs()); gone != "" {
		a.Label, a.Reason = LabelMissed, "the report never said "+quoteFragment(gone)
		return a
	}
	a.Label, a.Reason = LabelActed, firstLine(report)
	return a
}

// inheritChild is the child's environment: the researcher's own prompt and
// its read-only tools, rooted at the row's workspace, on the model the run
// names — and a record of every file it read.
type inheritChild struct {
	root  string
	p     provider.Provider
	model string

	mu    sync.Mutex
	reads []string
	usage provider.Usage
}

func (c *inheritChild) factory() subagent.EnvFactory {
	return func(ctx context.Context, spec subagent.Spec) (subagent.Env, error) {
		info := shell.Info{OS: runtime.GOOS, Cwd: c.root}
		defs := tools.Definitions()
		stream := func(msgs []provider.Message, choice string) (<-chan provider.StreamEvent, context.CancelFunc, error) {
			sctx, cancel := context.WithCancel(ctx)
			events, err := c.p.StreamCompletion(sctx, msgs, provider.CompletionOpts{
				Model: c.model, Tools: defs, ToolChoice: choice})
			if err != nil {
				cancel()
				return nil, nil, err
			}
			return c.count(events), cancel, nil
		}
		return subagent.Env{
			SystemPrompt: prompt.Inherited(prompt.BuildResearcher(info, prompt.WebTools{}), spec.Inherit),
			Stream:       stream,
			Executor:     subagent.RootedExecutor(c.root, c.note(tools.Execute)),
			ExecuteGated: func(name string, _ json.RawMessage) (string, error) {
				return "", errors.New(name + " is not available to this child")
			},
			RunCommand: func(context.Context, string) (string, int) { return "not available", 1 },
		}, nil
	}
}

// note records the path of every read_file call before it runs.
func (c *inheritChild) note(next agent.ToolExecutor) agent.ToolExecutor {
	return func(name string, args json.RawMessage) (string, error) {
		if name == "read_file" {
			var call struct {
				Path string `json:"path"`
			}
			if json.Unmarshal(args, &call) == nil {
				rel, err := filepath.Rel(c.root, call.Path)
				if err != nil || strings.HasPrefix(rel, "..") {
					rel = call.Path
				}
				c.mu.Lock()
				c.reads = append(c.reads, filepath.ToSlash(rel))
				c.mu.Unlock()
			}
		}
		return next(name, args)
	}
}

// count forwards a stream's events and adds up what they report spending.
func (c *inheritChild) count(in <-chan provider.StreamEvent) <-chan provider.StreamEvent {
	out := make(chan provider.StreamEvent)
	go func() {
		defer close(out)
		for ev := range in {
			if ev.Usage != nil {
				c.mu.Lock()
				c.usage.PromptTokens += ev.Usage.PromptTokens
				c.usage.CompletionTokens += ev.Usage.CompletionTokens
				c.mu.Unlock()
			}
			out <- ev
		}
	}()
	return out
}

func (c *inheritChild) used() provider.Usage {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.usage
}

// reread is the first of paths the child read, or "" when it read none of
// them.
func (c *inheritChild) reread(paths []string) string {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, want := range paths {
		want = filepath.ToSlash(filepath.Clean(want))
		for _, got := range c.reads {
			if filepath.ToSlash(filepath.Clean(got)) == want {
				return want
			}
		}
	}
	return ""
}
