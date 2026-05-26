package cmd

import (
	"os"

	"github.com/spf13/cobra"
)

var rootCmd = &cobra.Command{
	Use:   "invosit",
	Short: "File sync for gitignored files.",
	Long: `Invosit lets devs/teams push and pull gitignored files securely alongside a repository, with access control.

A small config file (.invosit.json) is committed to git; the actual file
bytes live in encrypted blob storage and are pulled down by teammates
via this CLI.`,
	SilenceUsage: true,
}

func Execute() {
	err := rootCmd.Execute()
	if err != nil {
		os.Exit(1)
	}
}

func init() {
	rootCmd.CompletionOptions.HiddenDefaultCmd = false // TODO: Make true in prod
}
