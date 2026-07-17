package initcmd

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/livecodelife/linespec/v3/pkg/config"
	"gopkg.in/yaml.v3"
)

// Options controls the behaviour of Run.
type Options struct {
	// ProjectPath is the directory to scan for framework/database hints.
	// If empty, Run prompts the user.
	ProjectPath string
	// OutputPath is the directory where .linespec.yml is written.
	// If empty, Run prompts the user (defaults to ProjectPath).
	OutputPath string
	// Force skips the overwrite confirmation when .linespec.yml already exists.
	Force bool
	// Stdin is read for interactive prompts. Defaults to os.Stdin.
	Stdin io.Reader
	// Stdout receives all prompt output. Defaults to os.Stdout.
	Stdout io.Writer
}

// Run executes the interactive init wizard and writes .linespec.yml.
func Run(opts Options) error {
	if opts.Stdin == nil {
		opts.Stdin = os.Stdin
	}
	if opts.Stdout == nil {
		opts.Stdout = os.Stdout
	}

	sc := bufio.NewScanner(opts.Stdin)

	// 1. Project path
	if opts.ProjectPath == "" {
		cwd, _ := os.Getwd()
		opts.ProjectPath = ask(sc, opts.Stdout, "Project path", cwd)
	}

	// 2. Scan project for defaults
	detected := scanProject(opts.ProjectPath)

	// 3. Feature mode
	mode := askChoice(sc, opts.Stdout, "Features", []string{"both", "testing", "provenance"}, "both")

	// 4. Service name (only needed for testing / both)
	serviceName := ""
	if mode != "provenance" {
		serviceName = ask(sc, opts.Stdout, "Service name", filepath.Base(opts.ProjectPath))
	}

	// 5. Framework (only needed for testing / both)
	framework := ""
	if mode != "provenance" {
		framework = askChoice(sc, opts.Stdout, "Framework", []string{"rails", "express", "chi", "django", "fastapi"}, detected.Framework)
	}

	// 6. Database (only needed for testing / both)
	dbType := ""
	if mode != "provenance" {
		dbType = askChoice(sc, opts.Stdout, "Database", []string{"mysql", "postgresql", "none"}, detected.Database)
	}

	// 7. Output path
	if opts.OutputPath == "" {
		opts.OutputPath = ask(sc, opts.Stdout, "Output directory for .linespec.yml", opts.ProjectPath)
	}

	// Guard against overwriting an existing config
	configPath := filepath.Join(opts.OutputPath, ".linespec.yml")
	if !opts.Force {
		if _, err := os.Stat(configPath); err == nil {
			answer := ask(sc, opts.Stdout, ".linespec.yml already exists. Overwrite? [y/N]", "N")
			if strings.ToLower(strings.TrimSpace(answer)) != "y" {
				fmt.Fprintln(opts.Stdout, "Aborted.")
				return nil
			}
		}
	}

	cfg := buildConfig(mode, serviceName, framework, dbType)

	if err := os.MkdirAll(opts.OutputPath, 0755); err != nil {
		return fmt.Errorf("cannot create output directory: %w", err)
	}

	data, err := yaml.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("failed to marshal config: %w", err)
	}

	if err := os.WriteFile(configPath, data, 0644); err != nil {
		return fmt.Errorf("failed to write config: %w", err)
	}

	fmt.Fprintf(opts.Stdout, "✓ Written %s\n", configPath)
	return nil
}

// detectedProject holds auto-detected hints from the project directory.
type detectedProject struct {
	Framework string
	Database  string
}

// scanProject inspects dir for well-known indicator files and returns defaults.
func scanProject(dir string) detectedProject {
	d := detectedProject{Database: "none"}

	switch {
	case fileExists(filepath.Join(dir, "Gemfile")):
		d.Framework = "rails"
	case fileExists(filepath.Join(dir, "manage.py")):
		d.Framework = "django"
	case fileExists(filepath.Join(dir, "requirements.txt")):
		d.Framework = "fastapi"
	case fileExists(filepath.Join(dir, "go.mod")):
		d.Framework = "chi"
	case fileExists(filepath.Join(dir, "package.json")):
		d.Framework = "express"
	}

	// database.yml (Rails convention) → mysql
	if fileExists(filepath.Join(dir, "config", "database.yml")) || fileExists(filepath.Join(dir, "database.yml")) {
		d.Database = "mysql"
		return d
	}

	// docker-compose.yml image name scan
	if db := scanDockerCompose(filepath.Join(dir, "docker-compose.yml")); db != "" {
		d.Database = db
	}

	return d
}

// scanDockerCompose reads a docker-compose file and returns the first recognised
// database image name, or an empty string if none is found.
func scanDockerCompose(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	lower := strings.ToLower(string(data))
	if strings.Contains(lower, "postgres") {
		return "postgresql"
	}
	if strings.Contains(lower, "mysql") {
		return "mysql"
	}
	return ""
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// ask prints a prompt with a default value and returns the user's input,
// falling back to defaultVal when the input is empty.
func ask(sc *bufio.Scanner, w io.Writer, question, defaultVal string) string {
	if defaultVal != "" {
		fmt.Fprintf(w, "%s [%s]: ", question, defaultVal)
	} else {
		fmt.Fprintf(w, "%s: ", question)
	}
	if sc.Scan() {
		if line := strings.TrimSpace(sc.Text()); line != "" {
			return line
		}
	}
	return defaultVal
}

// askChoice prints a prompt that lists valid options and loops until the user
// enters one of them (or accepts the default).
func askChoice(sc *bufio.Scanner, w io.Writer, question string, choices []string, defaultVal string) string {
	for {
		fmt.Fprintf(w, "%s [%s] (options: %s): ", question, defaultVal, strings.Join(choices, ", "))
		if sc.Scan() {
			line := strings.TrimSpace(sc.Text())
			if line == "" {
				return defaultVal
			}
			for _, c := range choices {
				if strings.EqualFold(line, c) {
					return c
				}
			}
			fmt.Fprintf(w, "  Invalid choice %q — pick one of: %s\n", line, strings.Join(choices, ", "))
		} else {
			return defaultVal
		}
	}
}

// buildConfig constructs a LineSpecConfig from the wizard answers.
func buildConfig(mode, serviceName, framework, dbType string) *config.LineSpecConfig {
	if mode == "provenance" {
		return &config.LineSpecConfig{
			Provenance: &config.ProvenanceConfig{
				Enforcement:       "strict",
				Dir:               "provenance",
				CommitTagRequired: true,
			},
		}
	}

	cfg := config.DefaultConfig(framework)
	cfg.Service.Name = serviceName

	// Apply database choice, overriding what DefaultConfig set.
	switch dbType {
	case "postgresql":
		cfg.Database = &config.DatabaseConfig{
			Type:  "postgresql",
			Image: "postgres:16",
			Port:  5432,
		}
		cfg.Infrastructure.Database = true
	case "mysql":
		if cfg.Database == nil {
			cfg.Database = &config.DatabaseConfig{
				Type:  "mysql",
				Image: "mysql:8.4",
				Port:  3306,
			}
		}
		cfg.Infrastructure.Database = true
	default:
		cfg.Database = nil
		cfg.Infrastructure.Database = false
	}

	if mode == "both" {
		cfg.Provenance = &config.ProvenanceConfig{
			Enforcement:       "strict",
			Dir:               "provenance",
			CommitTagRequired: true,
		}
	}

	return cfg
}
