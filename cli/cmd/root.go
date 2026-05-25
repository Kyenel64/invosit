package cmd

import (
	"os"

	"github.com/kyenel64/invosit/cli/internal/config"
	"github.com/spf13/cobra"
)

var configPathFlag string

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
	rootCmd.PersistentFlags().StringVar(&configPathFlag, "config", "", "path to .invosit.json (default: ./.invosit.json)")
}

func resolveConfigPath() string {
	if configPathFlag != "" {
		return configPathFlag
	}
	if found, err := config.Find("."); err == nil {
		return found
	}
	return config.FileName
}
