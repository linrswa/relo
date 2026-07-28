package cli

import (
	"fmt"
	"strings"

	"github.com/linrswa/relo/internal/store"
	"github.com/linrswa/relo/internal/upgrade"

	"github.com/spf13/cobra"
)

func (a *app) upgradeCmd() *cobra.Command {
	var requestedVersion string
	var check bool
	cmd := &cobra.Command{
		Use:   "upgrade",
		Short: "Upgrade relo to a newer release",
		Long:  "Check GitHub Releases and replace a manually installed relo executable after verifying its SHA-256 checksum. Package-manager-owned installations must be upgraded with their package manager.",
		Args:  validationArgs(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, args []string) error {
			versionSet := cmd.Flags().Changed("version")
			if check && versionSet {
				return store.ValidationError{Message: "--check and --version cannot be used together"}
			}
			if versionSet {
				if strings.TrimSpace(requestedVersion) == "" {
					return store.ValidationError{Message: "--version must not be empty"}
				}
				if _, err := upgrade.NormalizeVersion(requestedVersion); err != nil {
					return store.ValidationError{Message: err.Error()}
				}
			}

			result, err := upgrade.New(Version).Run(cmd.Context(), upgrade.Options{
				RequestedVersion: requestedVersion,
				Check:            check,
			})
			if err != nil {
				return err
			}
			if check {
				if result.UpdateAvailable {
					fmt.Fprintf(cmd.OutOrStdout(), "Update available: %s -> %s\n", result.CurrentVersion, result.Version)
				} else if result.CurrentVersion == result.Version {
					fmt.Fprintf(cmd.OutOrStdout(), "relo is up to date (%s)\n", result.Version)
				} else {
					fmt.Fprintf(cmd.OutOrStdout(), "No newer release available (current: %s, latest: %s)\n", result.CurrentVersion, result.Version)
				}
				return nil
			}
			if !result.Updated {
				if result.CurrentVersion == result.Version {
					fmt.Fprintf(cmd.OutOrStdout(), "relo is up to date (%s)\n", result.Version)
				} else {
					fmt.Fprintf(cmd.OutOrStdout(), "No newer release available (current: %s, latest: %s)\n", result.CurrentVersion, result.Version)
				}
				return nil
			}
			if versionSet {
				fmt.Fprintf(cmd.OutOrStdout(), "Installed relo %s (previously %s)\n", result.Version, result.CurrentVersion)
			} else {
				fmt.Fprintf(cmd.OutOrStdout(), "Upgraded relo from %s to %s\n", result.CurrentVersion, result.Version)
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&check, "check", false, "check for a newer release without installing it")
	cmd.Flags().StringVar(&requestedVersion, "version", "", "install a specific release version")
	return cmd
}
