package provider

import "sync/atomic"

// App names the program calling this package. Every string a backend writes
// that is the caller's rather than a vendor's comes from it: the environment
// variables a key and a base URL are read from, the prefix of every cache and
// routing key, the name OpenRouter is told, the group the plan route's tools
// travel in, and the sentence that says how to sign in to ChatGPT.
type App struct {
	// Name is the caller's name in lower case. It prefixes cache and
	// routing keys, names the tool group, and is the X-Title sent to
	// OpenRouter.
	Name string
	// EnvPrefix is the caller's environment prefix. "ACME" reads
	// ACME_API_KEY and ACME_BASE_URL.
	EnvPrefix string
	// SignInHint is the command a person runs to sign in to ChatGPT, as it
	// is typed, without quotes.
	SignInHint string
	// URL is sent to OpenRouter as HTTP-Referer. Empty sends no referer.
	URL string
}

// neutralApp is the identity in force until the caller sets its own.
var neutralApp = App{Name: "llm", EnvPrefix: "LLM"}

var app atomic.Pointer[App]

// SetApp names the caller. The app hands it its identity once at startup;
// until it does, and for any field it leaves empty, the package says "llm".
// Safe to call while requests are in flight.
func SetApp(a App) {
	if a.Name == "" {
		a.Name = neutralApp.Name
	}
	if a.EnvPrefix == "" {
		a.EnvPrefix = neutralApp.EnvPrefix
	}
	app.Store(&a)
}

// caller is the identity in force, never zero.
func caller() App {
	if a := app.Load(); a != nil {
		return *a
	}
	return neutralApp
}

// apiKeyVar and baseURLVar are the caller's own variables, read before the
// vendor's.
func apiKeyVar() string  { return caller().EnvPrefix + "_API_KEY" }
func baseURLVar() string { return caller().EnvPrefix + "_BASE_URL" }

// keyVars is how an error names the variables a key may come from.
func keyVars(vendor string) string { return apiKeyVar() + " or " + vendor }
