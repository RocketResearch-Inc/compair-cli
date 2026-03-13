package compair

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	"github.com/RocketResearch-Inc/compair-cli/internal/config"
)

var rootCmd = &cobra.Command{
	Use:   "compair",
	Short: "Compair CLI for code-aware insights",
}

func Execute() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func init() {
	cobra.OnInitialize(func() {
		viper.SetEnvPrefix("compair")
		viper.AutomaticEnv()
	})
	rootCmd.PersistentFlags().String("api-base", "", "API base URL (e.g., https://api.example.com)")
	rootCmd.PersistentFlags().String("profile", "", "Profile name to use for API base")
	rootCmd.PersistentFlags().String("group", "", "Active group to use (overrides COMPAIR_ACTIVE_GROUP)")
	rootCmd.PersistentFlags().Bool("verbose", false, "Enable verbose output")
	rootCmd.PersistentFlags().Bool("debug-http", false, "Log HTTP request details")
	rootCmd.PersistentFlags().Bool("no-color", false, "Disable colored output")
	_ = viper.BindPFlag("api.base", rootCmd.PersistentFlags().Lookup("api-base"))
	_ = viper.BindPFlag("profile", rootCmd.PersistentFlags().Lookup("profile"))
	_ = viper.BindPFlag("group", rootCmd.PersistentFlags().Lookup("group"))
	_ = viper.BindPFlag("verbose", rootCmd.PersistentFlags().Lookup("verbose"))
	_ = viper.BindPFlag("debug_http", rootCmd.PersistentFlags().Lookup("debug-http"))
	_ = viper.BindPFlag("no_color", rootCmd.PersistentFlags().Lookup("no-color"))
	rootCmd.PersistentPreRunE = resolveAPIBase
}

func flagValue(cmd *cobra.Command, name string) (string, bool) {
	if f := cmd.Flags().Lookup(name); f != nil && f.Changed {
		return f.Value.String(), true
	}
	if f := cmd.InheritedFlags().Lookup(name); f != nil && f.Changed {
		return f.Value.String(), true
	}
	return "", false
}

func resolveAPIBase(cmd *cobra.Command, _ []string) error {
	cliAPI, _ := flagValue(cmd, "api-base")
	cliProfile, _ := flagValue(cmd, "profile")
	base, source, profName, err := config.ResolveAPIBase(cliProfile, cliAPI)
	if err != nil {
		return err
	}
	if base == "" {
		if profName != "" {
			return fmt.Errorf("profile '%s' is not defined; use 'compair profile set %s --api-base <url>'", profName, profName)
		}
		base = "http://localhost:4000"
		source = "(fallback)"
	}
	viper.Set("api.base", base)
	if profName != "" {
		viper.Set("profile.active", profName)
	}
	if source != "" {
		viper.Set("profile.source", source)
	}
	if viper.GetBool("verbose") {
		_ = os.Setenv("COMPAIR_VERBOSE", "1")
	}
	if viper.GetBool("debug_http") || viper.GetBool("verbose") {
		_ = os.Setenv("COMPAIR_DEBUG_HTTP", "1")
	}
	return nil
}
