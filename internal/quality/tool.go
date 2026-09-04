package quality

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/rfizzle/shhh/internal/provider"
)

// ToolName is the model-facing quality-gate tool. It runs on the auto-run
// path without approval: the model can only name a suite from the config —
// every executable and argument comes from the user's .shhh/quality.json —
// and checks run read-only, contained when a mechanism is available. It is
// registered only where the person has trusted the checkout that file came
// with (docs/capabilities/approvals-and-safety.md#a-checkout-declares-what-it-runs).
const ToolName = "quality_gate"

// ToolDefinition is the quality_gate tool registered for agent sessions.
func ToolDefinition() provider.Tool {
	return provider.Tool{
		Name: ToolName,
		Description: "Verify your work with the repository's own quality checks (tests, linters). " +
			"Suites are named in the project's trusted config (" + ConfigRelPath + "); you pick a suite by name and can never supply command text. " +
			"Action \"run\" executes a suite (blocking; suite defaults to \"" + DefaultSuite + "\") and returns pass/fail/blocked/cancelled with each check's outcome. " +
			"Action \"result\" re-reports the last run and whether it is stale (the tree changed since). " +
			"Run the gate before declaring a task complete, and treat any verdict other than a non-stale pass as not done.",
		Parameters: json.RawMessage(`{
			"type": "object",
			"properties": {
				"action": {"type": "string", "enum": ["run", "result"], "description": "run: execute a suite now; result: re-report the last run with staleness"},
				"suite": {"type": "string", "description": "Named suite from the quality config (default: \"default\")"}
			},
			"required": ["action"]
		}`),
	}
}

type toolArgs struct {
	Action string `json:"action"`
	Suite  string `json:"suite"`
}

// ExecuteTool dispatches one quality_gate tool call.
func (r *Runner) ExecuteTool(args json.RawMessage) (string, error) {
	var a toolArgs
	if err := json.Unmarshal(args, &a); err != nil {
		return "", fmt.Errorf("invalid arguments: %w", err)
	}
	switch a.Action {
	case "run":
		res, err := r.Run(context.Background(), a.Suite)
		if err != nil {
			return "", err
		}
		return res.Format(res.Fingerprint), nil
	case "result":
		return r.Status(), nil
	}
	return "", fmt.Errorf("unknown action %q (valid: run, result)", a.Action)
}

// WrapExecutor returns an executor that dispatches quality_gate calls and
// hands everything else to next.
func (r *Runner) WrapExecutor(next func(name string, args json.RawMessage) (string, error)) func(string, json.RawMessage) (string, error) {
	return func(name string, args json.RawMessage) (string, error) {
		if name == ToolName {
			return r.ExecuteTool(args)
		}
		return next(name, args)
	}
}
