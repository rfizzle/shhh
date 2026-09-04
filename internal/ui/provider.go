package ui

// The missing-provider card and what it offers (
// docs/interface/surfaces.md#the-recovery-row).
//
// This is one of only two surfaces in the product that earns a card, and it
// earns it for the same reason the other one does: the session cannot
// continue without an answer. Everything else a provider can do to you is a
// row.
//
// The card's job is to end the guessing. "SHHH_API_KEY is not set" tells you
// one thing that is not true of one place; this names every place shhh
// looked, what it found in each, and which of them is probably the fix. Then
// it offers the three things that can actually be done about it here.

import (
	"strconv"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/rfizzle/shhh/internal/provider"
	"github.com/rfizzle/shhh/internal/resolve"
	"github.com/rfizzle/shhh/internal/ui/components"
	"github.com/rfizzle/shhh/internal/ui/keys"
)

// The keys the card offers are keys.Setup.

// ProviderChoice is what the card resolved to. A zero value is a decline —
// esc, or a card with nothing to offer — and leaves the caller with the
// failure it came in with.
type ProviderChoice struct {
	// Provider is the provider to start on.
	Provider string
	// APIKey is the key to start it with, when one was pasted.
	APIKey string
	// BaseURL points an openai-compatible session at a local runtime.
	BaseURL string
	// Model is the model to start on, when the choice implies one.
	Model string
	// Save asks the caller to write the choice to the config file, so the
	// next session does not meet this card again.
	Save bool
	// Chosen reports that anything was chosen at all.
	Chosen bool
}

// setupStep is where the little state machine is.
type setupStep int

const (
	stepCard setupStep = iota
	stepPickProvider
	stepPasteKey
	stepSave
	stepDone
)

// ProviderSetup is the program the card runs in. It is three components in
// sequence — the card, a provider selector, a masked key prompt — plus the
// one question worth asking afterwards, which is whether to keep the answer.
type ProviderSetup struct {
	card      components.ProviderCard
	pick      components.Select
	secret    components.SecretPrompt
	save      components.Confirm
	step      setupStep
	width     int
	survey    resolve.Survey
	providers []string
	choice    ProviderChoice
	// picked records that the wizard chose the provider, which is what makes
	// an empty key meaningful: a gateway or a local runtime may need none,
	// while an empty key for the provider that just failed means "never mind".
	picked   bool
	quitting bool
}

// NewProviderSetup builds the card from a survey and the providers that could
// be started instead. The local offer appears only when something local
// actually answered: an offer that cannot be honoured is worse than no offer.
func NewProviderSetup(s resolve.Survey, providers []string) ProviderSetup {
	return ProviderSetup{
		card:      components.ProviderCard{Places: placesFor(s), Likely: s.Likely, Keys: setupKeys(s)},
		survey:    s,
		providers: providers,
		width:     80,
	}
}

// Choice is what the program resolved to, read after it exits.
func (m ProviderSetup) Choice() ProviderChoice { return m.choice }

// placesFor turns the survey into card rows. The survey's own wording is kept
// — it did the looking, so it is the thing that knows what it saw.
func placesFor(s resolve.Survey) []components.ProviderPlace {
	out := make([]components.ProviderPlace, 0, len(s.Places))
	for _, p := range s.Places {
		out = append(out, components.ProviderPlace{
			Label:    string(p.Kind),
			Emphasis: p.Finding,
			Detail:   p.Detail,
			Found:    p.Found,
		})
	}
	return out
}

// setupKeys are the offers. The local one is conditional; the other two are
// always available, because pasting a key and picking a provider are things
// this card can always do.
func setupKeys(s resolve.Survey) []components.KeyOffer {
	offers := []components.KeyOffer{
		{Key: keys.Bracket(keys.Setup.Wizard), Label: keys.Words(keys.Setup.Wizard)},
		{Key: keys.Bracket(keys.Setup.Paste), Label: keys.Words(keys.Setup.Paste)},
	}
	if s.LocalBaseURL != "" {
		label := "use the local model"
		if s.LocalModel != "" {
			label = "use " + s.LocalModel + " locally"
		}
		offers = append(offers, components.KeyOffer{Key: keys.Bracket(keys.Setup.Local), Label: label})
	}
	return offers
}

func (m ProviderSetup) Init() tea.Cmd { return nil }

func (m ProviderSetup) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		return m, nil
	case tea.KeyPressMsg:
		return m.key(msg)
	}
	return m, nil
}

// key advances the machine. Every step's esc is a decline of that step, and a
// decline at the first step ends the program with nothing chosen — esc
// dismisses, never destroys.
func (m ProviderSetup) key(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch m.step {
	case stepCard:
		done, result := m.card.Update(msg)
		if !done {
			return m, nil
		}
		switch result {
		case keys.Shown(keys.Setup.Wizard):
			m.pick = components.Select{Title: "Which provider", Options: providerOptions(m.providers)}
			m.step = stepPickProvider
			return m, nil
		case keys.Shown(keys.Setup.Paste):
			m.choice.Provider = m.survey.Provider
			m.secret = components.SecretPrompt{
				Prompt: "Paste a key for " + m.survey.Provider,
				Hint:   keyHintFor(m.survey),
			}
			m.step = stepPasteKey
			return m, nil
		case keys.Shown(keys.Setup.Local):
			// A local runtime needs no key and no wizard: it is already
			// answering, which is the whole of the offer.
			m.choice = ProviderChoice{
				Provider: "openai-compatible",
				BaseURL:  m.survey.LocalBaseURL,
				Model:    m.survey.LocalModel,
				Chosen:   true,
			}
			m.save = components.Confirm{Prompt: "Make " + m.survey.LocalBaseURL + " the default for new sessions?"}
			m.step = stepSave
			return m, nil
		}
		return m.finish()

	case stepPickProvider:
		done, res := m.pick.Update(msg)
		if !done {
			return m, nil
		}
		if res.Canceled || res.Index < 0 || res.Index >= len(m.providers) {
			m.step = stepCard
			return m, nil
		}
		m.choice.Provider = m.providers[res.Index]
		m.picked = true
		m.secret = components.SecretPrompt{
			Prompt: "Paste a key for " + m.choice.Provider,
			Hint:   "leave it empty for an endpoint that needs none",
		}
		m.step = stepPasteKey
		return m, nil

	case stepPasteKey:
		done, result := m.secret.Update(msg)
		if !done {
			return m, nil
		}
		key, _ := result.(string)
		if key == "" && !m.picked {
			// Nothing pasted for the provider that just failed: that is a
			// decline of the offer, not a session on an empty key.
			m.step = stepCard
			return m, nil
		}
		m.choice.APIKey = key
		m.choice.Chosen = true
		m.save = components.Confirm{Prompt: "Save this to the config file for new sessions?"}
		m.step = stepSave
		return m, nil

	case stepSave:
		done, result := m.save.Update(msg)
		if !done {
			return m, nil
		}
		m.choice.Save, _ = result.(bool)
		return m.finish()
	}
	return m.finish()
}

func (m ProviderSetup) finish() (tea.Model, tea.Cmd) {
	m.step = stepDone
	m.quitting = true
	return m, tea.Quit
}

// View is the frame. The setup card draws inline on stderr and asks the
// terminal for nothing, so the view carries content and no state.
func (m ProviderSetup) View() tea.View {
	return tea.NewView(m.screen())
}

func (m ProviderSetup) screen() string {
	if m.quitting {
		return ""
	}
	width := min(m.width-2, 84)
	if width < 20 {
		width = 20
	}
	switch m.step {
	case stepPickProvider:
		return m.pick.View(width) + "\n"
	case stepPasteKey:
		return m.secret.View(width) + "\n"
	case stepSave:
		return m.save.View(width) + "\n"
	}
	return m.card.View(width) + "\n"
}

// providerOptions lists the registered providers for the wizard's first step,
// each described by the model a session on it would start on — the part of
// the choice that is not already in the name.
func providerOptions(names []string) []components.SelectOption {
	opts := make([]components.SelectOption, 0, len(names))
	for _, name := range names {
		desc := ""
		if model := provider.Defaults(name).Model; model != "" {
			desc = "starts on " + model
		}
		opts = append(opts, components.SelectOption{Label: name, Desc: desc})
	}
	return opts
}

// keyHintFor names the variables the pasted key stands in for, read off the
// survey's own env row so the two cannot disagree.
func keyHintFor(s resolve.Survey) string {
	for _, p := range s.Places {
		if p.Kind == resolve.PlaceEnv {
			if p.Detail != "" {
				return strings.TrimSuffix(p.Detail, " — unset")
			}
			return p.Finding
		}
	}
	return ""
}

// PlainProviderReport is the same information for a terminal that cannot show
// a card — a pipe, a CI log, `shhh cmd < prompt`. It says everything the card
// says and offers nothing, because there is nobody there to press a key.
func PlainProviderReport(s resolve.Survey) string {
	var b strings.Builder
	b.WriteString("no model provider configured; shhh looked in " + strconv.Itoa(len(s.Places)) + " places:\n")
	for _, p := range s.Places {
		mark := "  ✗ "
		if p.Found {
			mark = "  ✓ "
		}
		line := mark + padLabel(string(p.Kind)) + p.Finding
		if p.Detail != "" {
			if p.Finding != "" {
				line += " — "
			}
			line += p.Detail
		}
		b.WriteString(line + "\n")
	}
	if s.Likely != "" {
		b.WriteString("\n" + s.Likely + "\n")
	}
	// The variable is the offer and the file key is not: a report read in a
	// CI log is read by the person who is about to write the setup down
	// somewhere it will be committed.
	// See docs/capabilities/secrets.md#where-a-value-comes-from.
	b.WriteString("\nRun shhh in a terminal for the setup wizard, or export SHHH_API_KEY — " +
		"`shhh config set provider.api_key_env SHHH_API_KEY` makes the file read it.\n")
	return b.String()
}

func padLabel(label string) string {
	for len(label) < 10 {
		label += " "
	}
	return label
}
