package provider

// The Decisions API: typed questions put over one piece of shared evidence,
// answered as probabilities rather than as prose. A bounded call that only
// ever wanted a yes or a no — the permission classifier — can ask it instead
// of a completion, and what comes back is a number to hold against a
// threshold rather than a reply to parse.
//
// The API is a public beta, so its request shape lives here and nowhere else,
// and the test that pins it names the guide it was written against. Which
// models offer it is declared, never inferred: the native OpenAI providers
// answer from a floor of their own below, a gateway profile from a model it
// declares, and a model nothing names does not offer it.
// See docs/capabilities/providers.md#a-bounded-call-asks-for-the-shape-of-its-answer.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"strings"

	openai "github.com/sashabaranov/go-openai"
)

// Decider is a provider that can put typed questions to a model through the
// Decisions API. It is optional, like ModelLister: a caller asserts it, and
// asks OffersDecisions for the model it means to use before it asks anything,
// because a provider that can speak the API still serves models that cannot.
type Decider interface {
	// OffersDecisions reports whether model is declared to answer the
	// Decisions API on this provider. False is the answer for every model
	// nothing declares.
	OffersDecisions(model string) bool
	// Decide sends one request and returns its typed answers.
	Decide(ctx context.Context, req DecisionRequest) (DecisionResult, error)
}

// QuestionType is the kind of question asked. Only a predicate is modelled:
// the API also has choice and score questions, and nothing in shhh asks one,
// so neither is spelled here until something does.
type QuestionType string

const QuestionPredicate QuestionType = "predicate"

// DecisionQuestion is one question. Instructions is the proposition the
// model weighs; the evidence it is weighed against is the request's Input,
// shared by every question, and never part of the instructions.
type DecisionQuestion struct {
	Type         QuestionType
	Name         string
	Instructions string
}

// DecisionRequest is one request: the model, the evidence, the questions.
type DecisionRequest struct {
	Model     string
	Input     string
	Questions []DecisionQuestion
}

// AnswerType is the kind of answer a question came back with: a predicate's
// probability, or a refusal.
type AnswerType string

const (
	AnswerPredicate AnswerType = "predicate"
	// AnswerRefusal is the model declining to answer. It is an answer of its
	// own kind and carries no probability: a refusal read as a zero would be
	// a confident no, and read as anything else a guess.
	AnswerRefusal AnswerType = "refusal"
)

// DecisionAnswer is one question's answer, by the question's name.
// Probability is meaningful only on a predicate answer.
type DecisionAnswer struct {
	Type        AnswerType
	Name        string
	Probability float64
}

// DecisionResult is what one request came back with.
type DecisionResult struct {
	Answers []DecisionAnswer
	// Usage is the input the request is billed for. The API bills input
	// tokens only, so CompletionTokens is always zero.
	Usage Usage
	// Counted reports that Usage is shhh's own count of the input it sent
	// rather than the provider's figure. The guide documents no usage block
	// on a response, so a response without one is billed at the estimate
	// rather than at nothing.
	Counted bool
}

// Answer is the answer to the question called name, if one came back.
func (r DecisionResult) Answer(name string) (DecisionAnswer, bool) {
	for _, a := range r.Answers {
		if a.Name == name {
			return a, true
		}
	}
	return DecisionAnswer{}, false
}

// decisionsFloor is the models the native OpenAI providers offer the API on,
// by the floor's own prefix rule. It is a list of its own rather than a field
// of the capability floor because offering it is a claim about an endpoint
// and a model together: the floor is read for a gateway's models as well, and
// a gateway that serves gpt-6-luna's completions need not serve its
// decisions.
var decisionsFloor = []string{"gpt-6-luna"}

// DecisionsOnFloor reports whether the native OpenAI providers offer the
// Decisions API on model.
func DecisionsOnFloor(model string) bool {
	name := strings.ToLower(strings.TrimSpace(model))
	if i := strings.LastIndex(name, "/"); i >= 0 {
		name = name[i+1:]
	}
	for _, prefix := range decisionsFloor {
		if strings.HasPrefix(name, prefix) {
			return true
		}
	}
	return false
}

// decisionsDeclared is each registered provider's answer to whether a model
// offers the API, for the surfaces that ask without building a provider —
// the doctor's row.
var decisionsDeclared = map[string]func(model string) bool{}

// RegisterDecisions records how a provider answers OffersDecisions, under its
// name. A profile registers its declared models; the native providers
// register the floor.
func RegisterDecisions(name string, offers func(model string) bool) {
	if offers == nil {
		delete(decisionsDeclared, normalizeName(name))
		return
	}
	decisionsDeclared[normalizeName(name)] = offers
}

// DecisionsDeclared reports whether the provider registered as name offers the
// Decisions API on model, as that provider would answer it.
func DecisionsDeclared(name, model string) bool {
	offers, ok := decisionsDeclared[normalizeName(name)]
	return ok && offers(model)
}

// decisionsWire is the request as the guide spells it; the test that pins
// it names the guide and the date it was read.
type decisionsWire struct {
	Model     string              `json:"model"`
	Input     string              `json:"input"`
	Questions []decisionsQuestion `json:"questions"`
}

type decisionsQuestion struct {
	Type         QuestionType `json:"type"`
	Name         string       `json:"name"`
	Instructions string       `json:"instructions"`
}

// decisionsReply is the response. The probability is a pointer so a
// predicate answer that carries none is told apart from one that said zero.
type decisionsReply struct {
	Answers []struct {
		Type        AnswerType `json:"type"`
		Name        string     `json:"name"`
		Probability *float64   `json:"probability"`
	} `json:"answers"`
	Usage *struct {
		InputTokens int `json:"input_tokens"`
	} `json:"usage"`
}

// maxDecisionsBody bounds the response read. An answer is a few dozen bytes
// per question.
const maxDecisionsBody = 1 << 20

// estimatedBytesPerToken is the rough count a request with no reported usage
// is billed at: the same four bytes a token the context meter estimates with.
const estimatedBytesPerToken = 4

// PostDecisions sends one request to {baseURL}/decisions over client, with
// the key as a bearer token, and names any failure the way every dialect
// names one, under the provider called name.
func PostDecisions(ctx context.Context, client *http.Client, baseURL, apiKey, name string, req DecisionRequest) (DecisionResult, error) {
	classify := newClassifier(name, "SHHH_API_KEY or OPENAI_API_KEY", apiKey)
	res, err := postDecisions(ctx, client, baseURL, apiKey, req)
	if err != nil {
		return DecisionResult{}, classify(err)
	}
	return res, nil
}

func postDecisions(ctx context.Context, client *http.Client, baseURL, apiKey string, req DecisionRequest) (DecisionResult, error) {
	if client == nil {
		client = &http.Client{}
	}
	wire := decisionsWire{Model: req.Model, Input: req.Input}
	counted := len(req.Input)
	for _, q := range req.Questions {
		wire.Questions = append(wire.Questions, decisionsQuestion{Type: q.Type, Name: q.Name, Instructions: q.Instructions})
		counted += len(q.Name) + len(q.Instructions)
	}
	body, err := json.Marshal(wire)
	if err != nil {
		return DecisionResult{}, err
	}
	endpoint := strings.TrimSuffix(baseURL, "/") + "/decisions"
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return DecisionResult{}, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	if apiKey != "" {
		httpReq.Header.Set("Authorization", "Bearer "+apiKey)
	}
	resp, err := client.Do(httpReq)
	if err != nil {
		return DecisionResult{}, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxDecisionsBody))
	if err != nil {
		return DecisionResult{}, err
	}
	if resp.StatusCode/100 != 2 {
		// The SDK's own error type, so the failure taxonomy reads the status
		// and the body exactly as it reads a refused completion's.
		return DecisionResult{}, &openai.RequestError{
			HTTPStatus:     resp.Status,
			HTTPStatusCode: resp.StatusCode,
			Err:            fmt.Errorf("decisions: %s", resp.Status),
			Body:           raw,
		}
	}
	var reply decisionsReply
	if err := json.Unmarshal(raw, &reply); err != nil {
		return DecisionResult{}, fmt.Errorf("decisions: unreadable response: %w", err)
	}
	var out DecisionResult
	for _, a := range reply.Answers {
		answer := DecisionAnswer{Type: a.Type, Name: a.Name}
		if a.Type == AnswerPredicate {
			// A predicate with no probability, or one outside the unit
			// interval, is not an answer anybody can hold against a
			// threshold, so the request is a failure rather than a guess.
			if a.Probability == nil || math.IsNaN(*a.Probability) || *a.Probability < 0 || *a.Probability > 1 {
				return DecisionResult{}, fmt.Errorf("decisions: answer %q carries no probability", a.Name)
			}
			answer.Probability = *a.Probability
		}
		out.Answers = append(out.Answers, answer)
	}
	if reply.Usage != nil {
		out.Usage = Usage{PromptTokens: reply.Usage.InputTokens}
	} else {
		out.Usage, out.Counted = Usage{PromptTokens: counted / estimatedBytesPerToken}, true
	}
	return out, nil
}
