package provider

import (
	"fmt"
	"strings"
)

type Factory func() (Provider, error)

var registry = map[string]Factory{}

func Register(name string, f Factory) {
	registry[name] = f
}

func normalizeName(name string) string {
	return strings.ReplaceAll(name, "_", "-")
}

func Resolve(name string) (Provider, error) {
	f, ok := registry[normalizeName(name)]
	if !ok {
		return nil, fmt.Errorf("unknown provider: %q", name)
	}
	return f()
}

func Available() []string {
	names := make([]string, 0, len(registry))
	for name := range registry {
		names = append(names, name)
	}
	return names
}
