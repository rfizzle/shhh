package cli

import (
	"github.com/spf13/cobra"
)

var version = "dev"

func NewRootCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "shhh [prompt]",
		Short:   "Natural language to shell commands",
		Long:    "Turn plain English into executable shell commands.",
		Version: version,
	}

	return cmd
}
