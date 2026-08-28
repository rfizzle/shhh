package profile

// Writing profiles back out as TOML, for the consolidation `shhh providers
// migrate` performs.
//
// This is a deliberate hand-rolled emitter rather than toml.Encoder. The file
// it produces is one a person has to keep editing for years — it is where
// gateway quirks live — so it has to come out in a fixed, readable order with
// zero-valued fields left off entirely. A struct encoder writes `api_key = ""`
// beside every real setting and orders nothing.

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// Encode renders profiles as the contents of a providers.toml: one
// [[provider]] block each, in the order given.
func Encode(profiles []Profile) string {
	var b strings.Builder
	b.WriteString("# shhh providers — every provider this machine talks to, one [[provider]] block each.\n")
	b.WriteString("# A [[provider.endpoint]] is an address inside a provider: it overrides only what\n")
	b.WriteString("# differs from the block above it, and claims the models it declares or matches.\n")
	for _, p := range profiles {
		b.WriteString("\n[[provider]]\n")
		encodeProfile(&b, p)
	}
	return b.String()
}

func encodeProfile(b *strings.Builder, p Profile) {
	writeString(b, "", "name", p.Name)
	writeString(b, "", "api", p.API)
	writeString(b, "", "base_url", p.BaseURL)
	writeString(b, "", "api_key", p.APIKey)
	writeString(b, "", "api_key_env", p.APIKeyEnv)
	writeString(b, "", "models_path", p.ModelsPath)

	encodeHeaders(b, "  ", "provider.headers", p.Headers)
	encodeModels(b, "  ", "provider.models", p.Models)
	encodeRules(b, "  ", "provider.rewrite", p.Rewrite)

	for _, e := range p.Endpoints {
		b.WriteString("\n  [[provider.endpoint]]\n")
		writeString(b, "  ", "label", e.Label)
		writeStrings(b, "  ", "match", e.Match)
		writeString(b, "  ", "api", e.API)
		writeString(b, "  ", "base_url", e.BaseURL)
		writeString(b, "  ", "api_key", e.APIKey)
		writeString(b, "  ", "api_key_env", e.APIKeyEnv)
		writeString(b, "  ", "models_path", e.ModelsPath)
		encodeHeaders(b, "    ", "provider.endpoint.headers", e.Headers)
		encodeModels(b, "    ", "provider.endpoint.models", e.Models)
		encodeRules(b, "    ", "provider.endpoint.rewrite", e.Rewrite)
	}
}

func encodeHeaders(b *strings.Builder, indent, table string, headers map[string]string) {
	if len(headers) == 0 {
		return
	}
	keys := make([]string, 0, len(headers))
	for k := range headers {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	fmt.Fprintf(b, "\n%s[%s]\n", indent, table)
	for _, k := range keys {
		writeString(b, indent, k, headers[k])
	}
}

func encodeModels(b *strings.Builder, indent, table string, models []Model) {
	for _, m := range models {
		fmt.Fprintf(b, "\n%s[[%s]]\n", indent, table)
		writeString(b, indent, "id", m.ID)
		writeInt(b, indent, "context_window", m.ContextWindow)
		writeInt(b, indent, "max_tokens", m.MaxTokens)
		if cost := encodeCost(m.Cost); cost != "" {
			fmt.Fprintf(b, "%s%s = %s\n", indent, "cost", cost)
		}
	}
}

// encodeCost writes the inline table model cards are quoted in, omitting the
// cache prices when they are not set — most gateways never publish them.
func encodeCost(c Cost) string {
	var parts []string
	for _, f := range []struct {
		key string
		val float64
	}{
		{"input", c.Input}, {"output", c.Output},
		{"cache_read", c.CacheRead}, {"cache_write", c.CacheWrite},
	} {
		if f.val != 0 {
			parts = append(parts, fmt.Sprintf("%s = %s", f.key, formatFloat(f.val)))
		}
	}
	if len(parts) == 0 {
		return ""
	}
	return "{ " + strings.Join(parts, ", ") + " }"
}

func encodeRules(b *strings.Builder, indent, table string, rules []Rule) {
	for _, r := range rules {
		fmt.Fprintf(b, "\n%s[[%s]]\n", indent, table)
		if r.When.Model != "" {
			fmt.Fprintf(b, "%swhen = { model = %s }\n", indent, quote(r.When.Model))
		}
		if r.Direction != "" && r.Direction != DirectionRequest {
			writeString(b, indent, "direction", r.Direction)
		}
		writeString(b, indent, "op", r.Op)
		writeString(b, indent, "path", r.Path)
		if r.Value != nil {
			fmt.Fprintf(b, "%svalue = %s\n", indent, tomlValue(r.Value))
		}
		writeString(b, indent, "to", r.To)
		writeString(b, indent, "note", r.Note)
	}
}

func writeString(b *strings.Builder, indent, key, val string) {
	if val == "" {
		return
	}
	fmt.Fprintf(b, "%s%s = %s\n", indent, key, quote(val))
}

func writeStrings(b *strings.Builder, indent, key string, vals []string) {
	if len(vals) == 0 {
		return
	}
	quoted := make([]string, 0, len(vals))
	for _, v := range vals {
		quoted = append(quoted, quote(v))
	}
	fmt.Fprintf(b, "%s%s = [%s]\n", indent, key, strings.Join(quoted, ", "))
}

func writeInt(b *strings.Builder, indent, key string, val int64) {
	if val == 0 {
		return
	}
	fmt.Fprintf(b, "%s%s = %d\n", indent, key, val)
}

// tomlValue renders a rewrite rule's operand. TOML decoding hands back
// strings, booleans, int64, float64, and the nested shapes, and a rule's
// value can legitimately be any of them — `set` on a field whose upstream
// wants an object is a real use.
func tomlValue(v any) string {
	switch t := v.(type) {
	case string:
		return quote(t)
	case bool:
		return strconv.FormatBool(t)
	case int:
		return strconv.Itoa(t)
	case int64:
		return strconv.FormatInt(t, 10)
	case float64:
		return formatFloat(t)
	case []any:
		parts := make([]string, 0, len(t))
		for _, item := range t {
			parts = append(parts, tomlValue(item))
		}
		return "[" + strings.Join(parts, ", ") + "]"
	case map[string]any:
		keys := make([]string, 0, len(t))
		for k := range t {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		parts := make([]string, 0, len(keys))
		for _, k := range keys {
			parts = append(parts, fmt.Sprintf("%s = %s", k, tomlValue(t[k])))
		}
		return "{ " + strings.Join(parts, ", ") + " }"
	default:
		return quote(fmt.Sprint(v))
	}
}

// formatFloat keeps a price readable: 2 rather than 2.0000000001, and 0.15
// rather than 1.5e-01.
func formatFloat(f float64) string {
	return strconv.FormatFloat(f, 'g', -1, 64)
}

// quote writes a TOML basic string, escaping what the format requires.
func quote(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '"':
			b.WriteString(`\"`)
		case '\\':
			b.WriteString(`\\`)
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		case '\t':
			b.WriteString(`\t`)
		default:
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
	return b.String()
}
