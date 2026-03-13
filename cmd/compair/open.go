package compair

import (
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"

	"github.com/RocketResearch-Inc/compair-cli/internal/api"
	"github.com/RocketResearch-Inc/compair-cli/internal/config"
	"github.com/RocketResearch-Inc/compair-cli/internal/groups"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

var openCmd = &cobra.Command{
	Use:   "open",
	Short: "Open the active group in the web UI",
	RunE: func(cmd *cobra.Command, args []string) error {
		client := api.NewClient(viper.GetString("api.base"))
		gid, _, err := groups.ResolveWithAuto(client, "", viper.GetString("group"))
		if err != nil {
			return err
		}
		gcfg, _ := config.ReadGlobal()
		base := gcfg.APIBase
		if v := viper.GetString("api.base"); v != "" {
			base = v
		}
		if base == "" {
			base = "http://localhost:4000"
		}
		uiBase := strings.TrimSpace(osUIBase(base))
		url := uiBase + "/group/" + gid
		var ex *exec.Cmd
		switch runtime.GOOS {
		case "darwin":
			ex = exec.Command("open", url)
		case "windows":
			ex = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
		default:
			ex = exec.Command("xdg-open", url)
		}
		if err := ex.Start(); err != nil {
			fmt.Println("Open URL:", url)
		}
		return nil
	},
}

func init() { rootCmd.AddCommand(openCmd) }

func osUIBase(apiBase string) string {
	if env := strings.TrimSpace(os.Getenv("COMPAIR_UI_BASE")); env != "" {
		return strings.TrimRight(env, "/")
	}
	base := strings.TrimRight(strings.TrimSpace(apiBase), "/")
	if strings.HasSuffix(base, "/api") {
		base = strings.TrimSuffix(base, "/api")
	}
	return base
}
