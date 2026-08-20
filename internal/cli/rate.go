package cli

import (
	"bufio"
	"fmt"
	"os"
	"strings"

	"github.com/rfizzle/shhh/internal/storage"
	"github.com/spf13/cobra"
)

func newRateCmd() *cobra.Command {
	var limit int

	cmd := &cobra.Command{
		Use:   "rate",
		Short: "Rate past commands",
		Long:  "Walk through recent unrated commands and mark whether they actually worked, so accuracy metrics reflect real outcomes.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			db, err := storage.Open()
			if err != nil {
				return fmt.Errorf("open database: %w", err)
			}
			defer db.Close()

			entries, err := db.ListUnrated(limit)
			if err != nil {
				return fmt.Errorf("query unrated commands: %w", err)
			}
			if len(entries) == 0 {
				fmt.Println("Nothing to rate — all recent commands are rated.")
				return nil
			}

			fmt.Printf("%d unrated command(s). Did each one do what you wanted?\n", len(entries))
			fmt.Println("  [y] worked  [n] didn't work  [s] skip  [q] quit")
			fmt.Println()

			reader := bufio.NewReader(os.Stdin)
			rated := 0
			for i, e := range entries {
				fmt.Printf("%d/%d  %s\n", i+1, len(entries), e.CreatedAt.Local().Format("Jan 2 15:04"))
				fmt.Printf("  prompt:  %s\n", e.Prompt)
				fmt.Printf("  command: %s\n", e.Command)
				if e.ExitCode != nil {
					fmt.Printf("  exit:    %d\n", *e.ExitCode)
				}
				fmt.Print("  worked? [y/n/s/q] ")

				input, err := reader.ReadString('\n')
				if err != nil {
					break
				}
				switch strings.ToLower(strings.TrimSpace(input)) {
				case "y", "yes":
					if err := db.RateRequest(e.ID, true); err != nil {
						return err
					}
					rated++
				case "n", "no":
					if err := db.RateRequest(e.ID, false); err != nil {
						return err
					}
					rated++
				case "q", "quit":
					fmt.Printf("Rated %d command(s).\n", rated)
					return nil
				default:
					// skip
				}
				fmt.Println()
			}

			fmt.Printf("Rated %d command(s).\n", rated)
			return nil
		},
	}

	cmd.Flags().IntVarP(&limit, "limit", "n", 20, "max commands to review")
	return cmd
}
