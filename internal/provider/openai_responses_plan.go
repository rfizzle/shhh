package provider

// The ChatGPT plan route: the Responses wire code, paid for by a ChatGPT
// subscription instead of an API key. The bearer is an access token that
// moves under the session (TokenSource), the base URL is OpenAI's own and
// nothing else, and the request is held to the preview's limits — fields the
// route rejects are never sent, tools travel grouped, and the errors are a
// family of their own (failure.go). OpenAI never moves a refused request to
// another billing path, and neither does this route.
// See docs/capabilities/chatgpt-plan.md#what-a-plan-session-cannot-do.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const (
	chatGPTName = "chatgpt"
	// chatGPTKeyEnv is what the auth row offers in place of a variable to
	// set: the plan has no key, and the one remedy for a refused token is to
	// sign in again.
	chatGPTKeyEnv = "shhh providers login chatgpt"
	// chatGPTResolveWait bounds the token asked for while the provider is
	// built. A source that has to refresh first waits on a lock another
	// process may hold and then on the sign-in server; a minute is twice
	// the lock's own bound, and past it the session is better told it could
	// not start than left at a blank screen.
	chatGPTResolveWait = time.Minute
	// planToolNamespace is the one group shhh's tools travel in on this
	// route, and planToolNamespaceDescription what the model reads about the
	// group itself. The description says nothing about any tool: each
	// function inside keeps its own name and description, so the model is
	// told about the tools exactly what it is told on every other route.
	planToolNamespace            = "shhh"
	planToolNamespaceDescription = "The tools this session can call."
	// maxPlanCatalog bounds the model list read off the wire. The catalog is
	// a few dozen entries; a body past a megabyte is not one.
	maxPlanCatalog = 1 << 20
)

// ErrChatGPTSignedOut is the resolution failure of a plan session with no
// credential to send. It names the command, because the setup that fixes it
// is a sign-in in a browser, not a key anybody can paste.
var ErrChatGPTSignedOut = errors.New("not signed in to ChatGPT; run `shhh providers login chatgpt`")

// NewChatGPT builds the plan route. It reads no key from anywhere — not the
// flag, the environment or the config file — and points at OpenAI's own
// endpoint whatever the configuration says, because the token it sends is
// good for that endpoint alone and must not be handed to a gateway or a
// base URL somebody set for a key. HTTPClient is honoured as the transport
// seam only; the address stays fixed.
//
// The source is asked for a token here, once, so that a session with no
// usable sign-in fails to start with the sign-in's own words rather than at
// its first turn.
func NewChatGPT(opts ResolveOpts) (*OpenAIResponses, error) {
	if opts.TokenSource == nil {
		return nil, ErrChatGPTSignedOut
	}
	ctx, cancel := context.WithTimeout(context.Background(), chatGPTResolveWait)
	defer cancel()
	tok, err := opts.TokenSource.Token(ctx, "")
	if err != nil {
		return nil, err
	}
	if tok == "" {
		return nil, ErrChatGPTSignedOut
	}
	client := opts.HTTPClient
	if client == nil {
		client = &http.Client{}
	}
	p := &OpenAIResponses{
		client:   client,
		baseURL:  defaultResponsesBaseURL,
		model:    first(opts.Model, defaultResponsesModel),
		name:     chatGPTName,
		route:    routeChatGPTPlan,
		tokens:   opts.TokenSource,
		classify: newClassifier(chatGPTName, chatGPTKeyEnv, ""),
	}
	p.SetStreamIdle(opts.StreamIdleSeconds)
	return p, nil
}

// tokenFailure classifies a source that could not answer. A cancellation and
// a network failure are what they are; anything else is the credential, and
// reads as a rejected one so the auth row offers the sign-in. The source's
// own words are kept: they are what say whether the sign-in is missing or
// has expired.
func (o *OpenAIResponses) tokenFailure(err error) error {
	if errors.Is(err, context.Canceled) || isNetworkError(err) {
		return o.classify(err)
	}
	f := &Failure{
		Class:    ClassAuth,
		Provider: o.name,
		Message:  bound(err.Error()),
		KeyEnv:   chatGPTKeyEnv,
		err:      err,
	}
	record(f)
	return f
}

// shapeForPlan holds a built request to the plan route's limits, and is the
// only place that does: each limit is one line here, so a limit OpenAI
// lifts is a line deleted. It returns how a call's wire name maps back to
// shhh's, or nil when the request carries no tools.
//
// Three kinds of line. The docs require store off and streaming on, the
// history whole in input and the system prompt in instructions, which the
// request already does. The docs reject temperature and the output ceiling
// (and fields shhh never sets: truncation, metadata, user, top_p and the
// rest), so those go. And the docs say nothing either way about the prompt
// cache key and options, the text settings (verbosity, the structured
// output format), asking for the sealed reasoning, the reasoning effort, the
// tool choice and replayed reasoning items: until a call against the route
// has shown each is accepted, none is sent, because a field the route
// rejects fails the whole turn and a field left out only costs what it
// would have bought.
func shapeForPlan(req *responsesRequest) toolNamer {
	req.Store = false
	req.Stream = true

	req.Temperature = nil
	req.MaxOutput = 0

	req.PromptCacheKey = ""
	req.PromptCacheOptions = nil
	req.Text = nil
	req.Include = nil
	req.Reasoning = nil
	req.Input = planInput(req.Input)

	// With no tool choice to send, a request on which no tool may be called
	// carries no tools at all: that is the same promise in the one form the
	// route is known to take.
	choice := req.ToolChoice
	req.ToolChoice = ""
	if choice == ToolChoiceNone || len(req.Tools) == 0 {
		req.Tools = nil
		return nil
	}
	rename := planToolNames(req.Tools)
	// The route takes function tools only grouped in a namespace or added
	// as an input item; a namespace is the form the function-calling guide
	// documents and the one that keeps tools where every request puts them.
	req.Tools = []responsesTool{{
		Type:        "namespace",
		Name:        planToolNamespace,
		Description: planToolNamespaceDescription,
		Tools:       req.Tools,
	}}
	// A call the model made from the group goes back naming the group, the
	// way the model's own call item named it.
	for i := range req.Input {
		if req.Input[i].Type == "function_call" {
			req.Input[i].Namespace = planToolNamespace
		}
	}
	return rename
}

// planInput is the history as the plan route takes it: no replayed
// reasoning items (shapeForPlan), and no recording. The newest message's
// recording is refused before the request is built (planAttachments); one
// further back is a conversation that came from another provider, and it
// goes as a line saying what was left out rather than refusing every turn
// from here on for something nobody can now remove.
func planInput(items []responseItem) []responseItem {
	out := make([]responseItem, 0, len(items))
	for _, item := range items {
		if item.Type == "reasoning" {
			continue
		}
		if hasAudioPart(item.Content) {
			content := make([]responseContent, len(item.Content))
			for i, part := range item.Content {
				if part.Type == "input_audio" {
					part = responseContent{Type: "input_text", Text: "[this recording was not sent: the ChatGPT plan takes no audio]"}
				}
				content[i] = part
			}
			item.Content = content
		}
		out = append(out, item)
	}
	return out
}

func hasAudioPart(parts []responseContent) bool {
	for _, part := range parts {
		if part.Type == "input_audio" {
			return true
		}
	}
	return false
}

// planToolNames maps a call back to the tool shhh registered. The docs'
// shape keeps the function's own name and says the group beside it, so the
// name is usually already shhh's; a model that writes the group into the
// name instead ("shhh.read_file") is mapped back too, and only to a tool
// this request carried. Anything else is passed through as it came, and the
// session refuses it as an unknown tool the way it would on any route.
func planToolNames(tools []responsesTool) toolNamer {
	known := make(map[string]bool, len(tools))
	for _, t := range tools {
		known[t.Name] = true
	}
	return func(_, name string) string {
		if known[name] {
			return name
		}
		if local, ok := strings.CutPrefix(name, planToolNamespace+"."); ok && known[local] {
			return local
		}
		return name
	}
}

// planAttachments refuses the request whose newest message carries a
// recording, before anything is sent: the route takes no audio, and a turn
// the endpoint fails after the upload is the same refusal paid for twice.
// Images and documents travel inline as on the key route, and nothing shhh
// sends goes through the Files API, so audio is the one kind refused.
func (o *OpenAIResponses) planAttachments(messages []Message) error {
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role != RoleUser {
			continue
		}
		for _, a := range messages[i].Attachments {
			if a.Kind == AttachmentAudio {
				return o.refuse(ClassUnsupported, "input_audio",
					fmt.Sprintf("%s was not sent: a ChatGPT plan session cannot take audio", a.Name))
			}
		}
		return nil
	}
	return nil
}

// refuse is a request this route turns down itself, before it is sent. It
// is not written to the failure log: no request was refused by anybody but
// shhh, and the reason is on the screen.
func (o *OpenAIResponses) refuse(class Class, param, message string) *Failure {
	return &Failure{Class: class, Provider: o.name, Message: message, Param: param, KeyEnv: chatGPTKeyEnv}
}

// planHTTPError reads a refusal on the plan route, which comes in two
// shapes: the Responses error object, whose code and param say exactly what
// was refused, and a bare `{"detail": "…"}` from the admission layer in
// front of it, whose text is diagnostic only. Either way the status and the
// request id are kept.
func planHTTPError(resp *http.Response) error {
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
	failure := &apiError{
		Status:    resp.StatusCode,
		Message:   strings.TrimSpace(string(raw)),
		RequestID: resp.Header.Get("x-request-id"),
	}
	var body struct {
		Error struct {
			Message string `json:"message"`
			Code    string `json:"code"`
			Param   string `json:"param"`
		} `json:"error"`
		Detail json.RawMessage `json:"detail"`
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		return failure
	}
	switch {
	case body.Error.Message != "" || body.Error.Code != "":
		failure.Message = first(body.Error.Message, body.Error.Code)
		failure.Code = body.Error.Code
		failure.Param = body.Error.Param
	case len(body.Detail) > 0:
		var text string
		if json.Unmarshal(body.Detail, &text) == nil {
			failure.Message = text
		} else {
			failure.Message = string(body.Detail)
		}
	}
	return failure
}

// planModel is one entry of the plan's catalog. It is not the shape the
// key route's /v1/models answers: a slug to send, a name to show, and
// whether it is meant to be shown at all.
type planModel struct {
	Slug        string `json:"slug"`
	DisplayName string `json:"display_name"`
	Visibility  string `json:"visibility"`
}

// planModels is the signed-in account's catalog: the entries meant for a
// picker, in the order the server gave them, which is the order the docs
// ask a picker to keep. The slugs are the answer; the display names are
// kept for the picker to draw (ModelLabels).
func (o *OpenAIResponses) planModels(ctx context.Context) ([]string, error) {
	resp, err := o.send(ctx, http.MethodGet, "/models", nil, "application/json")
	if err != nil {
		return nil, o.classify(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, o.classify(planHTTPError(resp))
	}
	var body struct {
		Models *[]planModel `json:"models"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxPlanCatalog)).Decode(&body); err != nil {
		return nil, o.classify(fmt.Errorf("unrecognized catalog shape: %w", err))
	}
	if body.Models == nil {
		return nil, o.classify(errors.New("unrecognized catalog shape: no models list"))
	}
	var slugs []string
	labels := map[string]string{}
	for _, m := range *body.Models {
		if m.Visibility != "list" || m.Slug == "" || labels[m.Slug] != "" {
			continue
		}
		slugs = append(slugs, m.Slug)
		labels[m.Slug] = first(m.DisplayName, m.Slug)
	}
	o.labelsMu.Lock()
	o.labels = labels
	o.labelsMu.Unlock()
	return slugs, nil
}

// ModelLabels is the display name of each slug the last catalog listing
// returned, for a picker to show beside the slug it sends. It is empty
// until the catalog has been listed, and always on the key route.
func (o *OpenAIResponses) ModelLabels() map[string]string {
	o.labelsMu.Lock()
	defer o.labelsMu.Unlock()
	out := make(map[string]string, len(o.labels))
	for slug, label := range o.labels {
		out[slug] = label
	}
	return out
}

// ChatGPTDefaultModel is the model a plan session starts on, given the
// catalog the sign-in listed: the route's usual default where the plan
// serves it, and otherwise the first model the plan lists, since the server's
// order is its own recommendation. An empty catalog leaves the usual default.
func ChatGPTDefaultModel(listed []string) string {
	for _, slug := range listed {
		if slug == defaultResponsesModel {
			return slug
		}
	}
	if len(listed) > 0 {
		return listed[0]
	}
	return defaultResponsesModel
}
