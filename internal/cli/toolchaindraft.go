package cli

// The toolchain draft on the CLI side: the one request that reads the
// checkout into a declaration, the tool it answers through, the loader that
// judges the answer before anybody sees it, and the write behind the card's
// yes. The chat session is the only surface wired with it; a run with nobody
// to answer the card refuses the command in a sentence.
// See docs/capabilities/containment.md#a-declaration-can-be-drafted-for-you.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/rfizzle/shhh/internal/config"
	"github.com/rfizzle/shhh/internal/project"
	"github.com/rfizzle/shhh/internal/prompt"
	"github.com/rfizzle/shhh/internal/provider"
	"github.com/rfizzle/shhh/internal/ui/chat"
)

// toolchainDraftToolName is the tool a drafting answers through. It is
// offered in that one request and registered nowhere else.
const toolchainDraftToolName = project.ToolchainDraftToolName

// Bounds on one drafting. The ceiling carries the thought and the answer
// together, as every bounded call's does: a declaration is short, and the
// reading of a CI workflow that chose it is most of the budget.
const (
	toolchainDraftTimeout   = 120 * time.Second
	toolchainDraftMaxTokens = 8192
)

// toolchainDraftSchema is the tool's arguments. The model answers the
// declaration's four lists rather than the file's text, so it writes no TOML
// and cannot add a key; the file is rendered from the lists and read back by
// the loader, which is what judges the pins.
var toolchainDraftSchema = json.RawMessage(`{
	"type": "object",
	"properties": {
		"packages": {"type": "array", "items": {"type": "string"}, "description": "System packages from the sandbox image's own index (apk), alone or as name=version. Only for a tool that exists as a system package and nothing else; usually empty."},
		"install": {
			"type": "array",
			"description": "One entry per tool the checks run that the language's toolchain does not ship, each installed by one pinned command line.",
			"items": {
				"type": "object",
				"properties": {
					"line": {"type": "string", "description": "The install command at one exact version, in a form the grammar accepts, e.g. go install example.com/cmd/tool@v1.2.3."},
					"provides": {"type": "array", "items": {"type": "string"}, "description": "The binaries this line puts on PATH, named exactly as they appear under check."}
				},
				"required": ["line", "provides"]
			}
		},
		"hosts": {"type": "array", "items": {"type": "string"}, "description": "Registry host names the install lines download from, such as proxy.golang.org; no scheme, port or path."},
		"check": {"type": "array", "items": {"type": "string"}, "description": "Every binary the checks invoke beyond the language's own toolchain, named as it is found on PATH."},
		"changes": {
			"type": "array",
			"description": "Only when reviewing an existing declaration: each change you made to it with its reason. Empty when nothing needs to change.",
			"items": {
				"type": "object",
				"properties": {
					"change": {"type": "string", "description": "The change in a few words, such as: add gosec, or move golangci-lint to v2.5.0."},
					"reason": {"type": "string", "description": "The evidence in the checkout for it: the file and the line that show the change is needed."}
				},
				"required": ["change", "reason"]
			}
		}
	},
	"required": ["packages", "install", "hosts", "check"]
}`)

// toolchainDraftTool is the definition a drafting is offered, and the only
// one: the request can read nothing but what the session read for it and do
// nothing but answer.
func toolchainDraftTool() provider.Tool {
	return provider.Tool{
		Name:        toolchainDraftToolName,
		Description: "Return the drafted toolchain declaration — its four lists, what each install line provides, and for a review each change with its reason.",
		Parameters:  toolchainDraftSchema,
	}
}

// toolchainAnswer is the tool's arguments as read.
type toolchainAnswer struct {
	Packages []string `json:"packages"`
	Install  []struct {
		Line     string   `json:"line"`
		Provides []string `json:"provides"`
	} `json:"install"`
	Hosts   []string `json:"hosts"`
	Check   []string `json:"check"`
	Changes []struct {
		Change string `json:"change"`
		Reason string `json:"reason"`
	} `json:"changes"`
}

// declaration is the answer as the file's four lists.
func (a toolchainAnswer) declaration() project.Toolchain {
	tc := project.Toolchain{Packages: a.Packages, Hosts: a.Hosts, Check: a.Check}
	for _, in := range a.Install {
		tc.Install = append(tc.Install, strings.TrimSpace(in.Line))
	}
	return tc
}

// toolchainDrafter drafts a checkout's declaration on a provider.
type toolchainDrafter struct {
	prov  provider.Provider
	model func() string
	// root is the checkout the declaration belongs to and dir the directory
	// the session stands in, which the survey is taken of.
	root, dir string
	// scrub is the session's secret scrub. The evidence is the checkout's
	// own files — a workflow holding an inline token among them — and this
	// request is a door onto the provider like every other, so the text
	// passes it once, whole, before the request is built; nil is a session
	// with no secrets and sends it as read.
	// See docs/capabilities/secrets.md#the-value-is-scrubbed-at-every-door.
	scrub func(string) string
}

// draft runs one drafting: the request, the loader's reading of the answer,
// and — where the loader refuses it — the one retry that hands the model the
// loader's sentence. A second refusal is the drafting's failure, in the
// loader's words; nothing that would not load is ever handed to the card.
func (d toolchainDrafter) draft(ctx context.Context, review bool) chat.ToolchainDraft {
	prev, exists := project.Declared(d.root)
	if exists && prev == nil {
		return chat.ToolchainDraft{Err: project.ToolchainFile + " is there and cannot be read as a declaration — it is past the loader's bound or unreadable — so it is neither reviewed nor written over"}
	}
	review = review && exists
	evidence := toolchainDraftEvidence(project.Survey(d.dir), project.ReadDraftEvidence(d.root), prev, review)
	if d.scrub != nil {
		evidence = d.scrub(evidence)
	}
	msgs := []provider.Message{
		{Role: provider.RoleSystem, Content: prompt.ToolchainDraft(review)},
		{Role: provider.RoleUser, Content: evidence},
	}
	var refused error
	for attempt := 0; attempt < 2; attempt++ {
		raw, err := d.ask(ctx, msgs)
		if err != nil {
			return chat.ToolchainDraft{Err: err.Error()}
		}
		var ans toolchainAnswer
		if err := json.Unmarshal([]byte(raw), &ans); err != nil {
			refused = fmt.Errorf("the answer was not the draft_toolchain arguments: %w", err)
		} else {
			out, err := toolchainDraftOf(ans, prev, review)
			if err == nil {
				return out
			}
			if errors.Is(err, errNothingDeclared) {
				return chat.ToolchainDraft{Err: err.Error()}
			}
			refused = err
		}
		// The loader's own sentence, once: it names the entry and says what
		// a pinned one looks like, which is what the model needs to fix it.
		msgs = append(msgs,
			provider.Message{Role: provider.RoleAssistant, Content: raw},
			provider.Message{Role: provider.RoleUser, Content: "The loader refused that answer: " + refused.Error() +
				"\nAnswer again with " + toolchainDraftToolName + ", changing only what the refusal names."})
	}
	return chat.ToolchainDraft{Err: "the loader refused the draft twice — " + refused.Error()}
}

// toolchainDraftOf renders an answer into the file and reads it back through
// the loader, which is the whole of what makes a draft one the card may show.
func toolchainDraftOf(ans toolchainAnswer, prev []byte, review bool) (chat.ToolchainDraft, error) {
	content := ans.declaration().Render()
	read, err := project.ParseToolchain(content)
	if err != nil {
		return chat.ToolchainDraft{}, err
	}
	if len(read.Install)+len(read.Packages)+len(read.Check) == 0 {
		return chat.ToolchainDraft{}, errNothingDeclared
	}
	// Previous is whatever is there now, review or not: a file that appeared
	// since the session started is one the write would replace, and the card
	// has to show that as the change it is.
	out := chat.ToolchainDraft{Content: content, Previous: prev, Provides: map[string][]string{}}
	for _, in := range ans.Install {
		out.Provides[strings.TrimSpace(in.Line)] = in.Provides
	}
	if !review {
		return out, nil
	}
	for _, c := range ans.Changes {
		out.Changes = append(out.Changes, chat.ToolchainChange{Change: strings.TrimSpace(c.Change), Reason: strings.TrimSpace(c.Reason)})
	}
	// A review that answers the same four lists proposes nothing, whatever
	// its bytes: the card would be a diff of spacing.
	if was, err := project.ParseToolchain(prev); err == nil && sameDeclaration(was, read) {
		out.Unchanged = true
	}
	return out, nil
}

// errNothingDeclared is an answer naming no tool at all. It is not put back
// to the model: a checkout whose checks need nothing beyond the language's
// own toolchain has nothing to declare, and saying so is the answer.
var errNothingDeclared = errors.New("the checkout's checks name no tool beyond the language's own toolchain, so there is nothing to declare")

// sameDeclaration reports two declarations naming the same entries in the
// same order.
func sameDeclaration(a, b project.Toolchain) bool {
	return slices.Equal(a.Packages, b.Packages) && slices.Equal(a.Install, b.Install) &&
		slices.Equal(a.Hosts, b.Hosts) && slices.Equal(a.Check, b.Check)
}

// ask sends one request and returns the draft tool's arguments, or — from a
// model that answered in text — the object in its reply.
func (d toolchainDrafter) ask(ctx context.Context, msgs []provider.Message) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, toolchainDraftTimeout)
	defer cancel()
	events, err := d.prov.StreamCompletion(ctx, msgs, provider.CompletionOpts{
		Model:      d.model(),
		Flow:       provider.FlowToolchainDrafter,
		MaxTokens:  toolchainDraftMaxTokens,
		Tools:      []provider.Tool{toolchainDraftTool()},
		ToolChoice: provider.ToolChoiceAuto,
		Effort:     provider.EffortLow,
	})
	if err != nil {
		return "", err
	}
	var text strings.Builder
	for ev := range events {
		if ev.Err != nil {
			return "", ev.Err
		}
		text.WriteString(ev.Token)
		for _, tc := range ev.ToolCalls {
			if tc.Name == toolchainDraftToolName {
				return tc.Arguments, nil
			}
		}
		if ev.Done {
			break
		}
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	reply := text.String()
	i, j := strings.Index(reply, "{"), strings.LastIndex(reply, "}")
	if i < 0 || j < i {
		return "", errors.New("the model answered with no draft")
	}
	return reply[i : j+1], nil
}

// toolchainDraftEvidence is the request's user message: the survey's reading
// of the checkout, the files that say what its checks run, the lockfiles by
// name and the declaration as it stands. It is the checkout's own text, so
// it is framed as data the instruction says never to follow.
func toolchainDraftEvidence(info project.Info, ev project.DraftEvidence, prev []byte, review bool) string {
	var b strings.Builder
	b.WriteString("CHECKOUT\n")
	lang := info.Language
	if lang == "" {
		lang = "not recognised"
	}
	fmt.Fprintf(&b, "Language: %s", lang)
	if info.Toolchain != "" {
		fmt.Fprintf(&b, " (%s)", info.Toolchain)
	}
	b.WriteString("\n")
	if info.Packages > 0 {
		fmt.Fprintf(&b, "Packages: %d %s\n", info.Packages, info.Unit)
	}
	fmt.Fprintf(&b, "Repository: %t\n", info.Repo)
	if len(ev.Lockfiles) > 0 {
		fmt.Fprintf(&b, "Lockfiles (named, not shown): %s\n", strings.Join(ev.Lockfiles, ", "))
	}
	if len(ev.Files) == 0 {
		b.WriteString("\nNo build file, task runner, CI workflow or quality gate was found.\n")
	}
	for _, f := range ev.Files {
		cut := ""
		if f.Cut {
			cut = " (its head; the file is longer)"
		}
		fmt.Fprintf(&b, "\n--- %s%s ---\n%s\n", f.Path, cut, strings.TrimRight(f.Text, "\n"))
	}
	switch {
	case review:
		fmt.Fprintf(&b, "\n--- %s (the declaration as it stands) ---\n%s\n", project.ToolchainFile, strings.TrimRight(string(prev), "\n"))
		if _, err := project.ParseToolchain(prev); err != nil {
			fmt.Fprintf(&b, "The loader refuses it as it stands: %s\n", err)
		}
	default:
		fmt.Fprintf(&b, "\nThere is no %s yet.\n", project.ToolchainFile)
	}
	return b.String()
}

// writeToolchainDraft writes a drafted declaration into the checkout the way
// every file shhh writes is written — a synced temporary file renamed over
// the path — after reading it once more through the loader, so the one call
// that writes cannot write what the card could not have shown.
func writeToolchainDraft(root string) func([]byte) (string, error) {
	return func(content []byte) (string, error) {
		if root == "" {
			return "", errors.New("this session has no checkout to write it into")
		}
		if _, err := project.ParseToolchain(content); err != nil {
			return "", err
		}
		path := filepath.Join(root, filepath.FromSlash(project.ToolchainFile))
		// A link is refused by the loader, so a rename over one would write
		// a file the next session reads where this one pointed somewhere
		// else; the write replaces only a file.
		if info, err := os.Lstat(path); err == nil && !info.Mode().IsRegular() {
			return "", fmt.Errorf("%s is not a regular file, and the declaration has to be one", project.ToolchainFile)
		}
		if err := config.ReplaceFile(path, string(content), 0o644); err != nil {
			return "", err
		}
		toolchainMoves.Add(1)
		return project.ToolchainFile, nil
	}
}

// wireToolchainDraft gives the chat session's toolchain state the draft and
// the write. Only the chat session calls it — a run with nobody to answer
// the card, and every sub-agent, never has the command.
func wireToolchainDraft(tc *chat.Toolchain, prov provider.Provider, model func() string, scrub func(string) string) {
	dir, err := os.Getwd()
	if err != nil {
		return
	}
	t := projectTrust()
	root := t.Root
	if root == "" {
		root = project.Root(dir)
	}
	tc.Exists, tc.Untrusted = toolchainFileState(root)
	d := toolchainDrafter{prov: prov, model: model, root: root, dir: dir, scrub: scrub}
	tc.Draft = d.draft
	tc.WriteDraft = writeToolchainDraft(root)
	// Reading the declaration again reads these two again as well: the
	// card's "loads once trusted" line is about the trust answer as it
	// stands, which /trust can change under a waiting draft.
	if reread := tc.Reread; reread != nil {
		tc.Reread = func() chat.Toolchain {
			fresh := reread()
			fresh.Exists, fresh.Untrusted = toolchainFileState(root)
			return fresh
		}
	}
}

// toolchainFileState is whether the checkout holds a declaration, trusted or
// not, and whether the checkout is untrusted, so one would not load.
func toolchainFileState(root string) (exists, untrusted bool) {
	_, exists = project.Declared(root)
	return exists, !projectTrust().Allows()
}

// toolchainCommandRefusal is the sentence a run with nobody to answer the
// card gives /toolchain: a `-p` run and a served session would otherwise
// hand the words to the model as a prompt.
func toolchainCommandRefusal(prompt string) error {
	p := strings.TrimSpace(prompt)
	if p != "/toolchain" && !strings.HasPrefix(p, "/toolchain ") {
		return nil
	}
	return errors.New("/toolchain drafts a toolchain declaration onto a card a person answers, and this run has nobody to answer it: type /toolchain in a session")
}
