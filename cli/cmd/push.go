package cmd

import (
	"github.com/kyenel64/invosit/cli/internal/apiclient"
	"github.com/spf13/cobra"
)

var pushCmd = &cobra.Command{
	Use:   "push",
	Short: "Push files to encrypted blob storage",
	RunE: func(cmd *cobra.Command, args []string) error {
		creds, err := loadCredentials()
		if err != nil {
			return err
		}

		_ = apiclient.NewClient(creds.APIURL)
		return nil
	},
}

func init() {
	rootCmd.AddCommand(pushCmd)
}
