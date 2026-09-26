package resolve

import (
	"strings"
	"testing"
)

// Every combination of the four ranks that can name a model: the flag, the
// variable, the surface's own key and provider.model. The highest one set
// decides, ModelFrom names that rank, and ModelOutranks names it whenever it
// is above provider.model. With none of the four, the provider's default
// stands.
// See docs/capabilities/configuration.md#each-surface-can-have-a-model-of-its-own.
func TestResolve_EveryCombinationOfTheModelRanks(t *testing.T) {
	const surfaceKey = "provider.code_model"
	for mask := 0; mask < 16; mask++ {
		flag, env, surface, cfg := mask&8 != 0, mask&4 != 0, mask&2 != 0, mask&1 != 0
		t.Run(comboName(flag, env, surface, cfg), func(t *testing.T) {
			t.Setenv("SHHH_PROVIDER", "")
			t.Setenv("SHHH_MODEL", "")
			opts := Opts{SurfaceKey: surfaceKey}
			if flag {
				opts.FlagModel = "from-flag"
			}
			if env {
				t.Setenv("SHHH_MODEL", "from-env")
			}
			if surface {
				opts.SurfaceModel = "from-surface"
			}
			if cfg {
				opts.ConfigModel = "from-config"
			}

			model, from, over := defaultModels[DefaultProvider], "the provider's default", ""
			switch {
			case flag:
				model, from, over = "from-flag", "--model", "--model from-flag is on the command line"
			case env:
				model, from, over = "from-env", "SHHH_MODEL", "SHHH_MODEL is set to from-env"
			case surface:
				model, from, over = "from-surface", surfaceKey, surfaceKey+" is set to from-surface"
			case cfg:
				model, from = "from-config", "provider.model"
			}
			if got := Resolve(opts).Model; got != model {
				t.Errorf("model = %q, want %q", got, model)
			}
			if got := ModelFrom(opts); got != from {
				t.Errorf("ModelFrom = %q, want %q", got, from)
			}
			if got := ModelOutranks(opts); got != over {
				t.Errorf("ModelOutranks = %q, want %q", got, over)
			}
		})
	}
}

// A surface whose own key is unset resolves exactly as it did before surface
// keys existed: provider.model, and nothing reported above it.
func TestResolve_AnUnsetSurfaceKeyIsTheOldResolution(t *testing.T) {
	t.Setenv("SHHH_PROVIDER", "")
	t.Setenv("SHHH_MODEL", "")
	opts := Opts{SurfaceKey: "provider.cmd_model", ConfigModel: "from-config"}
	if got := Resolve(opts).Model; got != "from-config" {
		t.Fatalf("model = %q, want provider.model's", got)
	}
	if got := ModelOutranks(opts); got != "" {
		t.Fatalf("an unset surface key outranked provider.model: %q", got)
	}
}

func comboName(flag, env, surface, cfg bool) string {
	var on []string
	for _, r := range []struct {
		set  bool
		word string
	}{{flag, "flag"}, {env, "env"}, {surface, "surface"}, {cfg, "config"}} {
		if r.set {
			on = append(on, r.word)
		}
	}
	if len(on) == 0 {
		return "none"
	}
	return strings.Join(on, "+")
}
