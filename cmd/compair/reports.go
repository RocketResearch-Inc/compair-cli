package compair

import (
	"bufio"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	"github.com/charmbracelet/glamour"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

var reportsAll bool
var reportsSystem bool
var reportsFile string

type feedbackReport struct {
	Path    string
	ModTime int64
}

var reportsCmd = &cobra.Command{
	Use:   "reports",
	Short: "Browse saved feedback reports",
	RunE: func(cmd *cobra.Command, args []string) error {
		reports, err := discoverReports()
		if err != nil {
			return err
		}
		if len(reports) == 0 {
			return fmt.Errorf("no feedback reports found; run 'compair review' or 'compair sync' first")
		}

		if reportsFile != "" {
			path := reportsFile
			if !filepath.IsAbs(path) {
				path = filepath.Clean(path)
			}
			if _, err := os.Stat(path); err != nil {
				return fmt.Errorf("cannot read %s: %w", path, err)
			}
			reports = []feedbackReport{{Path: path}}
			reportsAll = false
		}

		if reportsSystem {
			return openWithSystem(reports[0].Path)
		}
		if reportsAll {
			return renderInteractive(reports)
		}
		return renderSingle(reports[0])
	},
}

func init() {
	rootCmd.AddCommand(reportsCmd)
	reportsCmd.Flags().BoolVar(&reportsAll, "all", false, "Iterate through all available feedback reports")
	reportsCmd.Flags().BoolVar(&reportsSystem, "system", false, "Open the latest report using the system default viewer")
	reportsCmd.Flags().StringVar(&reportsFile, "file", "", "Render a specific feedback report (overrides other options)")
}

func discoverReports() ([]feedbackReport, error) {
	dir := ".compair"
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	reports := make([]feedbackReport, 0, len(entries))
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(strings.ToLower(name), ".md") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		reports = append(reports, feedbackReport{
			Path:    filepath.Join(dir, name),
			ModTime: info.ModTime().UnixNano(),
		})
	}
	sort.Slice(reports, func(i, j int) bool { return reports[i].ModTime > reports[j].ModTime })
	return reports, nil
}

func renderSingle(report feedbackReport) error {
	data, err := os.ReadFile(report.Path)
	if err != nil {
		return err
	}
	fmt.Printf("\n== %s ==\n\n", filepath.Base(report.Path))
	out, err := renderMarkdown(string(data))
	if err != nil {
		fmt.Println(string(data))
		return nil
	}
	fmt.Print(out)
	return nil
}

func renderInteractive(reports []feedbackReport) error {
	reader := bufio.NewReader(os.Stdin)
	for idx, report := range reports {
		if err := renderSingle(report); err != nil {
			return err
		}
		if idx == len(reports)-1 {
			break
		}
		fmt.Printf("\n[%d/%d] Press Enter for next, 'o' to open in system viewer, 'q' to quit: ", idx+1, len(reports))
		input, _ := reader.ReadString('\n')
		choice := strings.TrimSpace(strings.ToLower(input))
		switch choice {
		case "o":
			if err := openWithSystem(report.Path); err != nil {
				fmt.Println("Error opening viewer:", err)
			}
		case "q":
			return nil
		}
	}
	return nil
}

func renderMarkdown(md string) (string, error) {
	if shouldRenderPlainMarkdown() {
		if strings.HasSuffix(md, "\n") {
			return md, nil
		}
		return md + "\n", nil
	}
	renderer, err := glamour.NewTermRenderer(glamour.WithAutoStyle())
	if err != nil {
		return "", err
	}
	return renderer.Render(md)
}

func shouldRenderPlainMarkdown() bool {
	if viper.GetBool("no_color") {
		return true
	}
	if strings.TrimSpace(os.Getenv("NO_COLOR")) != "" {
		return true
	}
	if runtime.GOOS != "windows" {
		return false
	}
	return strings.TrimSpace(os.Getenv("WT_SESSION")) == "" && strings.TrimSpace(os.Getenv("TERM_PROGRAM")) == ""
}

func openWithSystem(path string) error {
	switch runtime.GOOS {
	case "darwin":
		return exec.Command("open", path).Start()
	case "linux":
		return exec.Command("xdg-open", path).Start()
	case "windows":
		return exec.Command("cmd", "/c", "start", "", path).Start()
	default:
		return fmt.Errorf("system viewer not supported on %s", runtime.GOOS)
	}
}
