package cli

import (
	"strings"
	"testing"

	"github.com/rfizzle/shhh/internal/config"
)

// The flows row's classifier line names the backend, and on the decisions
// backend whether the model it resolved to offers the API and that a
// replaced wording is not sent there.
func TestDoctorFlows_NamesTheClassifiersBackend(t *testing.T) {
	var cfg config.Config
	if got := classifierBackendNote(cfg, "openai", "gpt-6-luna", true); got != " · completion backend" {
		t.Fatalf("default = %q", got)
	}
	cfg.Behavior.ClassifierBackend = "decisions"
	if got := classifierBackendNote(cfg, "openai", "gpt-6-luna", false); got != " · decisions backend, offered by this model" {
		t.Fatalf("offered = %q", got)
	}
	got := classifierBackendNote(cfg, "openai", "gpt-5.6-luna", true)
	for _, want := range []string{"not offered by this model", "fails closed", "prompts.classifier goes unused there"} {
		if !strings.Contains(got, want) {
			t.Fatalf("not offered, replaced = %q, missing %q", got, want)
		}
	}
	if got := classifierBackendNote(cfg, "my-gateway", "gpt-6-luna", false); !strings.Contains(got, "not offered") {
		t.Fatalf("a gateway nothing declared = %q", got)
	}
}

// The explanation reads with the classifier's model, except on the decisions
// backend: a model that answers the Decisions API may answer nothing else, so
// the explanation skips that link and takes the rest of the cheap chain. The
// classifier keeps its own key either way, and a key of the explainer's own
// still wins.
func TestExplainer_SkipsTheDecisionsModel(t *testing.T) {
	var cfg config.Config
	cfg.Behavior.ClassifierModel = "gpt-6-luna"
	cfg.Provider.CheapModel = "cheap"
	if m := resolveFlow(cfg, flowExplanation, "openai", "big").model; m != "gpt-6-luna" {
		t.Fatalf("completion backend: explanation on %q, want the classifier's model", m)
	}
	cfg.Behavior.ClassifierBackend = "decisions"
	if m := resolveFlow(cfg, flowExplanation, "openai", "big").model; m != "cheap" {
		t.Fatalf("decisions backend: explanation on %q, want the cheap chain", m)
	}
	if m := resolveFlow(cfg, flowClassifier, "openai", "big").model; m != "gpt-6-luna" {
		t.Fatalf("decisions backend: classifier on %q", m)
	}
	cfg.Behavior.ExplainerModel = "explainer"
	if m := resolveFlow(cfg, flowExplanation, "openai", "big").model; m != "explainer" {
		t.Fatalf("an explainer key: explanation on %q", m)
	}
}

// The backend's word is the agent package's, and a word it does not know is
// refused before it is written.
func TestConfigSet_JudgesTheClassifierBackend(t *testing.T) {
	if err := checkConfigValue("behavior.classifier_backend", "decisions"); err != nil {
		t.Fatalf("decisions: %v", err)
	}
	if err := checkConfigValue("behavior.classifier_backend", "oracle"); err == nil {
		t.Fatal("an unknown backend was accepted")
	}
	for _, v := range []string{"1", "80", "100"} {
		if err := checkConfigValue("behavior.classifier_threshold", v); err != nil {
			t.Errorf("threshold %s: %v", v, err)
		}
	}
	for _, v := range []string{"0", "-5", "101", "0.8"} {
		if err := checkConfigValue("behavior.classifier_threshold", v); err == nil {
			t.Errorf("threshold %s was accepted", v)
		}
	}
}
