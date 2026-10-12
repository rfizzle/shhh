package tools

import (
	"encoding/json"

	"github.com/rfizzle/shhh/internal/calltier"
)

// Tiers is the tier every tool this package defines declares for its calls,
// by tool name: the command at the command tier, and each file-modification
// tool at the write tier, carrying the paths its call names. The readers
// declare nothing, which is the read tier.
func Tiers() map[string]calltier.Declaration {
	out := map[string]calltier.Declaration{
		ExecCommandName: {Tier: calltier.Command, Kind: calltier.Run},
	}
	for _, d := range Mutating() {
		out[d.Tool.Name] = calltier.Declaration{
			Tier: calltier.Write,
			Kind: calltier.Edit,
			Contribute: func(name string, args json.RawMessage) calltier.Contribution {
				paths := WrittenPaths(name, string(args))
				if len(paths) == 0 {
					return calltier.Contribution{}
				}
				return calltier.Contribution{Path: paths[0], Paths: paths}
			},
		}
	}
	return out
}
