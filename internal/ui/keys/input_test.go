package keys

import (
	"strings"
	"testing"
)

// Every key the input answers has a paragraph in /help's key list. The
// input's row of the register is read off the offers, so a key added there
// with no words beside it is a key live at the draft that a reader who cannot
// find it has nowhere to look it up.
func TestEveryInputKeyHasHelp(t *testing.T) {
	helped := map[string]bool{}
	for _, o := range InputOffers() {
		if strings.TrimSpace(o.Help) == "" {
			for _, b := range o.Binds {
				t.Errorf("%q (%s) is live at the input with no paragraph in the key list", Shown(b), Words(b))
			}
			if len(o.Binds) == 0 {
				t.Errorf("the key list's row %q has no paragraph", o.Key)
			}
			continue
		}
		if len(o.Binds) == 0 && o.Key == "" {
			t.Errorf("a key list row binds nothing and names no key: %.40q", o.Help)
		}
		for _, b := range o.Binds {
			helped[Shown(b)] = true
		}
	}
	for _, b := range Surfaces()[0].Bindings {
		if !helped[Shown(b)] {
			t.Errorf("%q (%s) is live at the input with no paragraph in the key list", Shown(b), Words(b))
		}
	}
}

// The key list's order is each offer's weight, so two offers with one weight
// would read in whichever order they happen to be declared — the order the
// weight is there to stop the list depending on.
func TestInputOfferWeightsAreDistinct(t *testing.T) {
	seen := map[int]string{}
	for _, o := range InputOffers() {
		name := o.Key
		if len(o.Binds) > 0 {
			name = Shown(o.Binds[0])
		}
		if o.Weight <= 0 {
			t.Errorf("%q has no weight in the key list", name)
		}
		if prev, ok := seen[o.Weight]; ok {
			t.Errorf("%q and %q share the weight %d", prev, name, o.Weight)
		}
		seen[o.Weight] = name
	}
}
