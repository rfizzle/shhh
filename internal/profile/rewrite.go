package profile

// The rewrite DSL. A rule names a place in the JSON body and an edit to make
// there, optionally narrowed to some models. Rules run in file order against
// the decoded body, so later rules see earlier edits.
//
// Paths are dotted keys with "[]" meaning "every element of this array":
//
//	max_tokens                       a top-level field
//	chat_template_kwargs.enable_thinking
//	messages[].tool_call_id          a field on every message
//	messages[].tool_calls[].id       a field on every tool call of every message
//
// A path that doesn't exist is not an error — a rule that finds nothing to do
// does nothing, which is what makes a single profile safe across a mixed
// conversation where only some messages carry tool calls.

import (
	"fmt"
	"path"
	"strings"
)

// Rule directions.
const (
	DirectionRequest  = "request"
	DirectionResponse = "response"
)

// Rewrite operations.
const (
	OpDelete     = "delete"      // remove the field
	OpSet        = "set"         // set the field, replacing any value
	OpSetDefault = "set-default" // set the field only when absent or null
	OpRename     = "rename"      // move the field to `to` within the same object
	OpCutAt      = "cut-at"      // truncate a string at the first `value`
	OpTrimPrefix = "trim-prefix" // remove `value` from the start of a string
	OpTrimSuffix = "trim-suffix" // remove `value` from the end of a string
	OpReplace    = "replace"     // replace every `value` in a string with `to`
)

// Rule is one quirk: what to match, where to look, and what to do.
type Rule struct {
	// When narrows the rule; an empty When matches every request.
	When Match `toml:"when"`
	// Direction is "request" (the default) or "response". Response rules run
	// against a JSON body and against each SSE `data:` event.
	Direction string `toml:"direction"`
	// Op is the edit to perform.
	Op string `toml:"op"`
	// Path locates the field, e.g. "messages[].tool_calls[].id".
	Path string `toml:"path"`
	// Value is the operand: what to set, cut at, or trim.
	Value any `toml:"value"`
	// To is the second operand: the new key for rename, the replacement for
	// replace.
	To string `toml:"to"`
	// Note is a free-text reminder of why the quirk exists. Profiles outlive
	// the memory of the incident that caused them; `shhh providers` prints
	// this back.
	Note string `toml:"note"`
}

// Match narrows a rule to some requests.
type Match struct {
	// Model is a glob against the request's model ("gemini-*", "claude-*").
	Model string `toml:"model"`
}

func (r *Rule) validate() error {
	switch r.Direction {
	case "":
		r.Direction = DirectionRequest
	case DirectionRequest, DirectionResponse:
	default:
		return fmt.Errorf("direction %q is not %q or %q", r.Direction, DirectionRequest, DirectionResponse)
	}
	if r.Path == "" {
		return fmt.Errorf("path is required")
	}
	switch r.Op {
	case OpDelete:
	case OpSet, OpSetDefault:
		if r.Value == nil {
			return fmt.Errorf("op %q needs a value", r.Op)
		}
	case OpRename:
		if r.To == "" {
			return fmt.Errorf("op %q needs `to`", r.Op)
		}
	case OpCutAt, OpTrimPrefix, OpTrimSuffix:
		if _, ok := r.Value.(string); !ok {
			return fmt.Errorf("op %q needs a string value", r.Op)
		}
	case OpReplace:
		if _, ok := r.Value.(string); !ok {
			return fmt.Errorf("op %q needs a string value", r.Op)
		}
	case "":
		return fmt.Errorf("op is required")
	default:
		return fmt.Errorf("op %q is not a known operation", r.Op)
	}
	if _, err := path.Match(r.When.Model, ""); r.When.Model != "" && err != nil {
		return fmt.Errorf("when.model %q is not a valid pattern: %w", r.When.Model, err)
	}
	return nil
}

// matches reports whether the rule applies to a request for this model.
func (r Rule) matches(model string) bool {
	if r.When.Model == "" {
		return true
	}
	ok, err := path.Match(r.When.Model, model)
	return err == nil && ok
}

// Apply runs the rules of one direction over a decoded JSON body, reporting
// how many edits landed. The body is modified in place.
func Apply(rules []Rule, direction, model string, body map[string]any) int {
	edits := 0
	for _, rule := range rules {
		if rule.Direction != direction || !rule.matches(model) {
			continue
		}
		edits += rule.apply(body)
	}
	return edits
}

// apply walks the rule's path and edits every node it reaches. The ops that
// add a field build the objects on the way to it, so a rule can introduce a
// parameter the vanilla request has no place for — `reasoning.effort` on a
// request that carries no reasoning block. The ops that edit an existing
// value never create anything: a rule that finds nothing does nothing.
func (r Rule) apply(body map[string]any) int {
	segments := parsePath(r.Path)
	if len(segments) == 0 {
		return 0
	}
	create := r.Op == OpSet || r.Op == OpSetDefault
	edits := 0
	walk(body, segments[:len(segments)-1], create, func(obj map[string]any) {
		edits += r.edit(obj, segments[len(segments)-1].key)
	})
	return edits
}

// edit performs the operation on one field of one object.
func (r Rule) edit(obj map[string]any, key string) int {
	cur, present := obj[key]
	switch r.Op {
	case OpDelete:
		if !present {
			return 0
		}
		delete(obj, key)
		return 1
	case OpSet:
		obj[key] = r.Value
		return 1
	case OpSetDefault:
		if present && cur != nil {
			return 0
		}
		obj[key] = r.Value
		return 1
	case OpRename:
		if !present {
			return 0
		}
		delete(obj, key)
		obj[r.To] = cur
		return 1
	}

	// The remaining operations are string surgery.
	s, ok := cur.(string)
	if !ok {
		return 0
	}
	operand, _ := r.Value.(string)
	var next string
	switch r.Op {
	case OpCutAt:
		idx := strings.Index(s, operand)
		if idx == -1 {
			return 0
		}
		next = s[:idx]
	case OpTrimPrefix:
		next = strings.TrimPrefix(s, operand)
	case OpTrimSuffix:
		next = strings.TrimSuffix(s, operand)
	case OpReplace:
		next = strings.ReplaceAll(s, operand, r.To)
	default:
		return 0
	}
	if next == s {
		return 0
	}
	obj[key] = next
	return 1
}

// segment is one step of a path: a key, and whether it indexes into an array.
type segment struct {
	key   string
	array bool
}

// parsePath splits "messages[].tool_calls[].id" into its segments.
func parsePath(p string) []segment {
	var out []segment
	for _, part := range strings.Split(p, ".") {
		if part == "" {
			continue
		}
		seg := segment{key: part}
		if strings.HasSuffix(part, "[]") {
			seg.key = strings.TrimSuffix(part, "[]")
			seg.array = true
		}
		out = append(out, seg)
	}
	return out
}

// walk visits every object reachable through the given path prefix, calling
// fn on each. Missing keys and unexpected types end that branch quietly.
// With create set, a missing object along the way is built rather than
// ending the branch; an existing value of the wrong type is still left
// alone, and a missing array is never invented — there is no way to know how
// many elements a rule meant.
func walk(node any, segments []segment, create bool, fn func(map[string]any)) {
	obj, ok := node.(map[string]any)
	if !ok {
		return
	}
	if len(segments) == 0 {
		fn(obj)
		return
	}
	seg := segments[0]
	child, present := obj[seg.key]
	if !present || child == nil {
		if !create || seg.array {
			return
		}
		child = map[string]any{}
		obj[seg.key] = child
	}
	if !seg.array {
		walk(child, segments[1:], create, fn)
		return
	}
	items, ok := child.([]any)
	if !ok {
		return
	}
	for _, item := range items {
		walk(item, segments[1:], create, fn)
	}
}
