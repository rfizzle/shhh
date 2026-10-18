package cli

import "github.com/rfizzle/shhh/internal/provider"

// shhhApp is what the provider layer writes into the variables it reads, the
// keys it caches under and the sign-in sentence it prints. The root command
// hands it over before any provider is resolved (root.go), and the tests that
// resolve one without running a command set it in their TestMain.
var shhhApp = provider.App{
	Name:       "shhh",
	EnvPrefix:  "SHHH",
	SignInHint: "shhh providers login chatgpt",
	URL:        "https://github.com/rfizzle/shhh",
}
