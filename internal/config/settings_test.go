package config

import (
	"reflect"
	"strings"
	"testing"
)

// structKeys is every key the file can hold, read off the struct rather than
// off the table, so the two can be compared without either being the source
// of the other.
//
// The two maps are left out and each for its own reason. `mcp.servers` is a
// table with eight fields and a name the person picks, and `shhh mcp` is the
// surface that knows that shape. `agents.profiles` is covered by the table's
// one wildcard entry, which is checked separately below.
func structKeys(t *testing.T) map[string]reflect.Type {
	t.Helper()
	out := map[string]reflect.Type{}
	var walk func(reflect.Type, string)
	walk = func(rt reflect.Type, prefix string) {
		for i := 0; i < rt.NumField(); i++ {
			f := rt.Field(i)
			name, _, _ := strings.Cut(f.Tag.Get("toml"), ",")
			switch name {
			case "-":
				continue
			case "":
				name = f.Name
			}
			key, ft := prefix+name, f.Type
			for ft.Kind() == reflect.Pointer {
				ft = ft.Elem()
			}
			switch {
			case ft.Kind() == reflect.Struct:
				walk(ft, key+".")
			case ft.Kind() == reflect.Map:
				continue
			default:
				out[key] = f.Type
			}
		}
	}
	walk(reflect.TypeOf(Config{}), "")
	return out
}

// The table and the struct hold each other up. A field with no entry is a
// setting no surface can reach — the failure that left over half of them
// readable only by opening the file — and an entry with no field is one that
// would accept a write and change nothing.
func TestSettings_TableAndStructAgree(t *testing.T) {
	fields := structKeys(t)
	inTable := map[string]bool{}
	for _, s := range settings {
		if strings.Contains(s.Key, RoleWildcard) {
			continue
		}
		inTable[s.Key] = true
		if _, ok := fields[s.Key]; !ok {
			t.Errorf("%s is in the table and names no field", s.Key)
		}
	}
	for key := range fields {
		if !inTable[key] {
			t.Errorf("%s is a field no table entry reaches", key)
		}
	}
}

// Kind is what a surface decides how to draw a value from, and the field's Go
// type is what the parse comes from. They are two statements about one
// setting, so a row whose kind says "word" over an integer field would draw a
// picker onto a number.
func TestSettings_KindMatchesTheFieldType(t *testing.T) {
	fields := structKeys(t)
	want := map[Kind][]string{
		KindString: {"string"},
		KindEnum:   {"string"},
		KindPath:   {"string"},
		KindEnvVar: {"string"},
		KindList:   {"[]string"},
		KindBool:   {"bool", "*bool"},
		KindInt:    {"int", "int64", "*int"},
	}
	for _, s := range settings {
		ft, ok := fields[s.Key]
		if !ok {
			continue // the wildcard entry, checked in its own test
		}
		got := ft.String()
		if !containsString(want[s.Kind], got) {
			t.Errorf("%s is %s in the struct and %v in the table", s.Key, got, s.Kind)
		}
	}
}

func containsString(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

// Every setting can be written and read back through the table alone, which
// is the property that lets the screen, the listing and the write all work
// off it without knowing any key by name.
func TestSettings_EveryKeyRoundTrips(t *testing.T) {
	sample := map[Kind]string{
		KindString: "x",
		KindEnum:   "x",
		KindPath:   "/tmp/x.md",
		KindEnvVar: "SHHH_TEST_KEY",
		KindList:   "a, b",
		KindBool:   "true",
		KindInt:    "7",
	}
	for _, s := range settings {
		key := strings.ReplaceAll(s.Key, RoleWildcard, "reviewer")
		var cfg Config
		if err := Set(&cfg, key, sample[s.Kind]); err != nil {
			t.Errorf("Set(%q): %v", key, err)
			continue
		}
		got, set := Value(cfg, key)
		if !set {
			t.Errorf("%s reads back as unset after it was written", key)
			continue
		}
		if s.Kind == KindList && got != "a, b" {
			t.Errorf("%s reads back as %q", key, got)
		}
	}
}

// An unset config reads every key as unset, which is what the screen and the
// listing lean on to say "default" rather than printing a zero.
func TestSettings_NothingIsSetInAnEmptyConfig(t *testing.T) {
	var cfg Config
	for _, s := range settings {
		key := strings.ReplaceAll(s.Key, RoleWildcard, "reviewer")
		if _, set := Value(cfg, key); set {
			t.Errorf("%s reads as set in an empty config", key)
		}
	}
}

// Every setting says what stands when nothing sets it and what it decides.
// A row with neither is a row a reader learns nothing from.
func TestSettings_EveryEntryStatesItsDefaultAndItsPurpose(t *testing.T) {
	for _, s := range settings {
		if s.Default == "" {
			t.Errorf("%s states no default", s.Key)
		}
		if s.Desc == "" {
			t.Errorf("%s says nothing about what it decides", s.Key)
		}
		if s.Kind == KindEnum && len(s.Values) == 0 {
			t.Errorf("%s is a word and names none", s.Key)
		}
	}
}

// The wildcard is the whole of the per-role story: any role name resolves, a
// role nobody named is untouched, and the pattern itself is not a key.
func TestSettings_RoleWildcard(t *testing.T) {
	if _, ok := Lookup("agents.profiles.archaeologist.model"); !ok {
		t.Error("a role nobody wrote a case for is not settable")
	}
	if _, ok := Lookup("agents.profiles..model"); ok {
		t.Error("the empty role should not resolve")
	}
	if _, ok := Lookup("agents.profiles." + RoleWildcard + ".model"); ok {
		t.Error("the pattern itself should not be a settable key")
	}
	s, ok := Lookup("agents.profiles.reviewer.model")
	if !ok || s.Key != "agents.profiles.reviewer.model" {
		t.Errorf("a resolved key should carry the role, got %q", s.Key)
	}
}

// The keys with a rank above the file are the five the documentation names,
// and no others: a key that claimed an environment variable nothing reads
// would put a source in the listing that never happens.
func TestSettings_OnlyTheProviderKeysOutrankTheFile(t *testing.T) {
	want := map[string]string{
		"provider.default":   "SHHH_PROVIDER",
		"provider.model":     "SHHH_MODEL",
		"provider.api_key":   "SHHH_API_KEY",
		"provider.base_url":  "SHHH_BASE_URL",
		"provider.reasoning": "SHHH_REASONING",
	}
	for _, s := range settings {
		if s.Env == "" {
			continue
		}
		if want[s.Key] != s.Env {
			t.Errorf("%s reads %s, which is not one of the ranks above the file", s.Key, s.Env)
		}
		delete(want, s.Key)
	}
	for key := range want {
		t.Errorf("%s has lost its environment rank", key)
	}
}

// A Config is copied by value and its maps are not, so a write through one
// copy must not reach the other. The config screen holds two — what it loaded
// and what it has staged — and a shared profiles map would make a staged role
// model read as already written, and then not write it.
func TestSet_AWriteDoesNotReachAnotherCopy(t *testing.T) {
	var loaded Config
	if err := Set(&loaded, "agents.profiles.reviewer.model", "haiku"); err != nil {
		t.Fatal(err)
	}
	staged := loaded
	if err := Set(&staged, "agents.profiles.reviewer.model", "opus"); err != nil {
		t.Fatal(err)
	}
	if got, _ := Value(loaded, "agents.profiles.reviewer.model"); got != "haiku" {
		t.Fatalf("the loaded copy changed under the staged one: %q", got)
	}
	if err := Set(&staged, "agents.profiles.writer.model", "sonnet"); err != nil {
		t.Fatal(err)
	}
	if _, set := Value(loaded, "agents.profiles.writer.model"); set {
		t.Fatal("a role added to one copy appeared in the other")
	}
}

// Reading a per-role key that nothing has set leaves the config as it was: a
// listing that walks every role must not create the roles it walked.
func TestValue_ReadingARoleDoesNotCreateIt(t *testing.T) {
	var cfg Config
	if _, set := Value(cfg, "agents.profiles.reviewer.model"); set {
		t.Fatal("an unset role reads as set")
	}
	if cfg.Agents.Profiles != nil {
		t.Fatalf("reading created the profiles map: %v", cfg.Agents.Profiles)
	}
}
