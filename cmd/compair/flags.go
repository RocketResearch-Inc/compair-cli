package compair

import "github.com/spf13/cobra"

func getIntFlagIfChanged(cmd *cobra.Command, name string) (int, bool) {
	if f := cmd.Flags().Lookup(name); f != nil && f.Changed {
		v, err := cmd.Flags().GetInt(name)
		if err != nil {
			return 0, false
		}
		return v, true
	}
	return 0, false
}

func getStringArrayFlagIfChanged(cmd *cobra.Command, name string) ([]string, bool) {
	if f := cmd.Flags().Lookup(name); f != nil && f.Changed {
		v, err := cmd.Flags().GetStringArray(name)
		if err != nil {
			return nil, false
		}
		return v, true
	}
	return nil, false
}

func getStringFlagIfChanged(cmd *cobra.Command, name string) (string, bool) {
	if f := cmd.Flags().Lookup(name); f != nil && f.Changed {
		v, err := cmd.Flags().GetString(name)
		if err != nil {
			return "", false
		}
		return v, true
	}
	return "", false
}
