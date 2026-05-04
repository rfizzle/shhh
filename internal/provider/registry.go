package provider

import "fmt"

type Factory func() (Provider, error)

var registry = map[string]Factory{}

func Register(name string, f Factory) {
	registry[name] = f
}

func Resolve(name string) (Provider, error) {
	f, ok := registry[name]
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
