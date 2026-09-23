package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"sort"
	"strings"

	"github.com/spf13/cobra"
)

// errorClassSchema is the word both JSON shapes put on `error_class` for a
// run whose answer did not satisfy the schema it was handed. It sits beside
// the provider's own classes because the reader is the same: a consumer that
// branches on the field has to tell an answer of the wrong shape from a
// provider that stopped answering, and both leave the run with a 4.
// See docs/capabilities/headless.md#an-answer-can-be-held-to-a-schema.
const errorClassSchema = "schema"

// schemaKeywords are the keywords the validator checks, and annotations are
// the ones it may read past because they say nothing about what is valid.
// Anything else is refused when the schema is read: a keyword the validator
// skipped would be a promise the run said it kept and did not.
var (
	schemaKeywords = map[string]bool{"type": true, "required": true, "properties": true, "enum": true}
	annotations    = map[string]bool{"$schema": true, "$id": true, "$comment": true, "title": true, "description": true, "examples": true, "default": true}
	schemaTypes    = map[string]bool{"object": true, "array": true, "string": true, "number": true, "integer": true, "boolean": true, "null": true}
)

// answerSchema is the shape an unattended run's final answer is held to: the
// file as the model is shown it, and the tree the answer is checked against.
type answerSchema struct {
	text string
	root *schemaNode
}

// schemaNode is one schema object reduced to the four keywords.
type schemaNode struct {
	types      []string
	required   []string
	properties map[string]*schemaNode
	enum       []any
}

// loadAnswerSchema reads --output-schema before anything is resolved, so a
// schema the run could never be held to — unreadable, not JSON, a keyword it
// does not check, an enum no value of its type could satisfy — stops the
// command before a provider is asked anything.
func loadAnswerSchema(path string) (*answerSchema, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("--output-schema: %w", err)
	}
	root, err := parseSchemaNode(raw, "$")
	if err != nil {
		return nil, fmt.Errorf("--output-schema %s: %w", path, err)
	}
	var compact bytes.Buffer
	if err := json.Compact(&compact, raw); err != nil {
		return nil, fmt.Errorf("--output-schema %s: %w", path, err)
	}
	return &answerSchema{text: compact.String(), root: root}, nil
}

// addOutputSchemaFlag registers --output-schema on the two commands whose
// unattended run can be held to one. Like --output it implies --print: a
// schema is a promise about what stdout carries, and a session has no stdout
// a script reads.
func addOutputSchemaFlag(cmd *cobra.Command, path *string) {
	cmd.Flags().StringVar(path, "output-schema", "", "with --print, a JSON Schema file the final answer must satisfy (type, required, properties, enum): the answer is checked, asked for once more on a miss, and stated as JSON (implies --print)")
}

// outputSchema is the flag's value read, or nil where it was not given.
func outputSchema(path string) (*answerSchema, error) {
	if path == "" {
		return nil, nil
	}
	return loadAnswerSchema(path)
}

func parseSchemaNode(raw []byte, at string) (*schemaNode, error) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil || fields == nil {
		return nil, fmt.Errorf("%s: a schema is a JSON object", at)
	}
	keys := make([]string, 0, len(fields))
	for k := range fields {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if !schemaKeywords[k] && !annotations[k] {
			return nil, fmt.Errorf("%s: %q is not a keyword this run checks (type, required, properties, enum)", at, k)
		}
	}
	n := &schemaNode{}
	if t, ok := fields["type"]; ok {
		var one string
		if json.Unmarshal(t, &one) == nil {
			n.types = []string{one}
		} else if json.Unmarshal(t, &n.types) != nil || len(n.types) == 0 {
			return nil, fmt.Errorf("%s: type is a name or a list of names", at)
		}
		for _, name := range n.types {
			if !schemaTypes[name] {
				return nil, fmt.Errorf("%s: %q is not a JSON type", at, name)
			}
		}
	}
	if r, ok := fields["required"]; ok {
		if json.Unmarshal(r, &n.required) != nil {
			return nil, fmt.Errorf("%s: required is a list of property names", at)
		}
	}
	if p, ok := fields["properties"]; ok {
		var props map[string]json.RawMessage
		if json.Unmarshal(p, &props) != nil || props == nil {
			return nil, fmt.Errorf("%s: properties is an object of schemas", at)
		}
		n.properties = make(map[string]*schemaNode, len(props))
		for name, sub := range props {
			child, err := parseSchemaNode(sub, at+"."+name)
			if err != nil {
				return nil, err
			}
			n.properties[name] = child
		}
	}
	if e, ok := fields["enum"]; ok {
		v, err := decodeJSON(e)
		list, isList := v.([]any)
		if err != nil || !isList || len(list) == 0 {
			return nil, fmt.Errorf("%s: enum is a non-empty list of values", at)
		}
		n.enum = list
		// A list none of whose values is of the type beside it is a schema
		// no answer can satisfy, and a run held to it would spend its one
		// hand-back finding that out.
		possible := len(n.types) == 0
		for _, want := range n.enum {
			if n.typeFits(want) {
				possible = true
			}
		}
		if !possible {
			return nil, fmt.Errorf("%s: no value in enum is of type %s", at, strings.Join(n.types, " or "))
		}
	}
	return n, nil
}

// instruction is the prompt's closing instruction, which is how the model is
// told the shape before it has written anything.
func (s *answerSchema) instruction() string {
	return "\n\nYour final answer must be a single JSON value, with no prose and no code fence around it, that satisfies this JSON Schema:\n" + s.text
}

// schemaMiss is an answer the schema refused, with every reason the
// validator found. It is the run's error on a second miss, which the record
// files as a failed turn and the exit code projects to 4.
type schemaMiss struct{ problems []string }

func (e *schemaMiss) Error() string {
	return "the answer does not satisfy --output-schema: " + strings.Join(e.problems, "; ")
}

// check reads an answer as JSON and holds it to the schema, returning the
// answer as the JSON the transcript states it as. A fence around the whole
// answer is read past, because a model told not to write one sometimes does
// and the value inside it is still the answer.
func (s *answerSchema) check(answer string) (json.RawMessage, error) {
	text := strings.TrimSpace(answer)
	if len(text) > 6 && strings.HasPrefix(text, "```") && strings.HasSuffix(text, "```") {
		text = strings.TrimSpace(strings.TrimSuffix(text, "```"))
		if nl := strings.IndexByte(text, '\n'); nl >= 0 {
			text = strings.TrimSpace(text[nl+1:])
		}
	}
	v, err := decodeJSON([]byte(text))
	if err != nil {
		return nil, &schemaMiss{problems: []string{"the answer is not a JSON value: " + err.Error()}}
	}
	var problems []string
	s.root.validate(v, "$", &problems)
	if len(problems) > 0 {
		return nil, &schemaMiss{problems: problems}
	}
	var compact bytes.Buffer
	if err := json.Compact(&compact, []byte(text)); err != nil {
		return nil, &schemaMiss{problems: []string{err.Error()}}
	}
	return compact.Bytes(), nil
}

// decodeJSON reads exactly one JSON value, with numbers kept as written so an
// integer is told from a number by its value rather than by a float's.
func decodeJSON(raw []byte) (any, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return nil, err
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return nil, errors.New("more than one value")
	}
	return v, nil
}

func (n *schemaNode) validate(v any, at string, problems *[]string) {
	if len(n.types) > 0 && !n.typeFits(v) {
		*problems = append(*problems, fmt.Sprintf("%s: want %s, got %s", at, strings.Join(n.types, " or "), jsonTypeOf(v)))
		return
	}
	if len(n.enum) > 0 {
		found := false
		for _, want := range n.enum {
			if jsonEqual(v, want) {
				found = true
				break
			}
		}
		if !found {
			*problems = append(*problems, at+": not one of the enum's values")
		}
	}
	obj, ok := v.(map[string]any)
	if !ok {
		return
	}
	for _, name := range n.required {
		if _, ok := obj[name]; !ok {
			*problems = append(*problems, fmt.Sprintf("%s: %q is required", at, name))
		}
	}
	names := make([]string, 0, len(n.properties))
	for name := range n.properties {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		if sub, ok := obj[name]; ok {
			n.properties[name].validate(sub, at+"."+name, problems)
		}
	}
}

func (n *schemaNode) typeFits(v any) bool {
	got := jsonTypeOf(v)
	for _, want := range n.types {
		if want == got || (want == "number" && got == "integer") {
			return true
		}
	}
	return false
}

// jsonTypeOf names a decoded value's JSON type, with a number that has no
// fractional part named integer — the one type the schema's vocabulary splits
// that JSON itself does not.
func jsonTypeOf(v any) string {
	switch x := v.(type) {
	case nil:
		return "null"
	case bool:
		return "boolean"
	case string:
		return "string"
	case []any:
		return "array"
	case map[string]any:
		return "object"
	case json.Number:
		if f, err := x.Float64(); err == nil && f == math.Trunc(f) {
			return "integer"
		}
		return "number"
	}
	return "unknown"
}

func jsonEqual(a, b any) bool {
	switch x := a.(type) {
	case json.Number:
		y, ok := b.(json.Number)
		if !ok {
			return false
		}
		fa, errA := x.Float64()
		fb, errB := y.Float64()
		return errA == nil && errB == nil && fa == fb
	case []any:
		y, ok := b.([]any)
		if !ok || len(x) != len(y) {
			return false
		}
		for i := range x {
			if !jsonEqual(x[i], y[i]) {
				return false
			}
		}
		return true
	case map[string]any:
		y, ok := b.(map[string]any)
		if !ok || len(x) != len(y) {
			return false
		}
		for k, xv := range x {
			yv, ok := y[k]
			if !ok || !jsonEqual(xv, yv) {
				return false
			}
		}
		return true
	}
	return a == b
}

// schemaClose is the close hook a schema adds: the answer is checked where
// the turn would end, and a miss is handed back once — as the user message
// the close gate's verdict is handed back as, carrying the validator's own
// reasons. A second miss lets the turn end, and the run then fails on it.
// See docs/capabilities/headless.md#an-answer-can-be-held-to-a-schema.
type schemaClose struct {
	schema *answerSchema
	// truncated reports the answer being cut at the output ceiling, which is
	// refused whatever it parses as: half an object is not an answer, and a
	// cut number is still a number.
	truncated func() bool
	asked     bool
}

func (s *schemaClose) close(final string) string {
	_, err := s.check(final)
	if err == nil || s.asked {
		return ""
	}
	s.asked = true
	var miss *schemaMiss
	errors.As(err, &miss)
	return "Your answer does not satisfy the JSON Schema this run was given: " +
		strings.Join(miss.problems, "; ") +
		". Answer again with only a JSON value that satisfies it: no prose and no code fence."
}

// check is the answer's standing against the schema, the ceiling included,
// and the answer as JSON where it stands.
func (s *schemaClose) check(final string) (json.RawMessage, error) {
	if s.truncated != nil && s.truncated() {
		return nil, &schemaMiss{problems: []string{"the answer stopped at the model's output ceiling"}}
	}
	return s.schema.check(final)
}
