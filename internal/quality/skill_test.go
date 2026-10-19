package quality

import (
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/rfizzle/agentcore/skill"
)

// The binary's own instructions for this file are held to the structs that
// read it, so the shape the model is taught cannot drift from the one that
// loads. See docs/capabilities/skills.md#shhh-carries-skills-of-its-own.

// gateSkillBody is the body of the built-in the refusal and the tool
// description name, which fails the test if no built-in has that name.
func gateSkillBody(t *testing.T) string {
	t.Helper()
	for _, s := range skill.Builtins(os.DirFS("../cli")) {
		if s.Name == gateSkill {
			body, err := s.Body()
			if err != nil {
				t.Fatal(err)
			}
			return body
		}
	}
	t.Fatal("no built-in shhh-quality-gate skill")
	return ""
}

// jsonBlocks are the fenced JSON examples in a Markdown body, in order.
func jsonBlocks(body string) []string {
	var out []string
	for _, part := range strings.Split(body, "```json\n")[1:] {
		block, _, _ := strings.Cut(part, "```")
		out = append(out, block)
	}
	return out
}

// Every example in the skill loads through the same reader and validation a
// checkout's file does, the starter first.
func TestTheGateSkillsExamplesLoad(t *testing.T) {
	blocks := jsonBlocks(gateSkillBody(t))
	if len(blocks) < 2 {
		t.Fatalf("the skill carries a starter and a second toolchain; found %d examples", len(blocks))
	}
	for i, block := range blocks {
		dir := t.TempDir()
		path := filepath.Join(dir, filepath.FromSlash(ConfigRelPath))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(block), 0o644); err != nil {
			t.Fatal(err)
		}
		cfg, err := LoadConfig(dir)
		if err != nil {
			t.Errorf("example %d does not load: %v\n%s", i+1, err, block)
			continue
		}
		if _, ok := cfg.Suites[DefaultSuite]; !ok {
			t.Errorf("example %d has no %q suite", i+1, DefaultSuite)
		}
	}
}

// jsonKeys is every json tag on the struct types given. They are named
// rather than walked, because the file's shape is exactly these four.
func jsonKeys(types ...reflect.Type) map[string]bool {
	keys := map[string]bool{}
	for _, typ := range types {
		for i := 0; i < typ.NumField(); i++ {
			name, _, _ := strings.Cut(typ.Field(i).Tag.Get("json"), ",")
			if name != "" && name != "-" {
				keys[name] = true
			}
		}
	}
	return keys
}

// The skill names every key the file can hold, and its field list (a key
// written as `"key":`) names none the file cannot.
func TestTheGateSkillNamesEveryKeyAndNoOther(t *testing.T) {
	body := gateSkillBody(t)
	keys := jsonKeys(reflect.TypeOf(Config{}), reflect.TypeOf(Suite{}), reflect.TypeOf(Check{}), reflect.TypeOf(Generator{}))
	var missing []string
	for k := range keys {
		if !strings.Contains(body, "`\""+k+"\"`") {
			missing = append(missing, k)
		}
	}
	sort.Strings(missing)
	if len(missing) > 0 {
		t.Errorf("the skill's field list does not name %v", missing)
	}
	for _, m := range regexp.MustCompile("`\"([a-z_]+)\"`:").FindAllStringSubmatch(body, -1) {
		if !keys[m[1]] {
			t.Errorf("the skill lists %q, which no struct of the file reads", m[1])
		}
	}
	for _, placeholder := range []string{PackagesPlaceholder, pkgPlaceholder, ScopePackages} {
		if !strings.Contains(body, placeholder) {
			t.Errorf("the skill does not say %s", placeholder)
		}
	}
}
