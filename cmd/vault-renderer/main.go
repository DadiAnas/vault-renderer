package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/dadianas/vault-renderer/internal/renderer"
)

var version = "dev"

func main() { os.Exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr)) }

func run(args []string, in io.Reader, out, diag io.Writer) int {
	command := "render"
	if len(args) > 0 && (args[0] == "render" || args[0] == "validate" || args[0] == "inspect" || args[0] == "version" || args[0] == "helm2-chart") {
		command = args[0]
		args = args[1:]
	}
	if command == "version" {
		fmt.Fprintln(out, version)
		return 0
	}
	flags := flag.NewFlagSet(command, flag.ContinueOnError)
	flags.SetOutput(diag)
	configPath := flags.String("config", os.Getenv("VAULT_RENDERER_CONFIG"), "renderer config or mixed Helm values YAML file")
	outputDir := flags.String("output-dir", "", "output directory for helm2-chart")
	namespace := flags.String("namespace", os.Getenv("VAULT_RENDERER_NAMESPACE"), "effective Helm release namespace")
	dryRun := flags.Bool("dry-run", false, "validate rendered manifests without writing them")
	strict := flags.Bool("strict", true, "reject unknown settings")
	debug := flags.Bool("debug", false, "write diagnostic summary to stderr")
	logLevel := flags.String("log-level", "error", "error or debug")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if len(flags.Args()) != 0 {
		fmt.Fprintln(diag, "unexpected positional arguments")
		return 2
	}
	if !*strict {
		fmt.Fprintln(diag, "--strict=false is unsupported: unknown settings always fail closed")
		return 2
	}
	if *logLevel != "error" && *logLevel != "debug" {
		fmt.Fprintln(diag, "--log-level must be error or debug")
		return 2
	}
	cfg := renderer.DefaultConfig()
	if *configPath != "" {
		configRoot, err := os.OpenRoot(filepath.Dir(*configPath))
		if err != nil {
			fmt.Fprintln(diag, err)
			return 1
		}
		data, err := configRoot.ReadFile(filepath.Base(*configPath))
		closeErr := configRoot.Close()
		if err != nil {
			fmt.Fprintln(diag, err)
			return 1
		}
		if closeErr != nil {
			fmt.Fprintln(diag, closeErr)
			return 1
		}
		cfg, err = renderer.ParseConfig(data)
		if err != nil {
			fmt.Fprintln(diag, err)
			return 1
		}
	}
	if command == "validate" {
		fmt.Fprintln(out, "configuration valid")
		return 0
	}
	if command == "inspect" {
		fmt.Fprintf(out, "enabled: %t\ntargets: %d\nexclusions: %d\n", cfg.Injector.Enabled, len(cfg.Injector.Targets), len(cfg.Injector.Exclude))
		return 0
	}
	input, err := io.ReadAll(in)
	if err != nil {
		fmt.Fprintln(diag, err)
		return 1
	}
	result, err := renderer.Render(input, cfg, *namespace)
	if err != nil {
		fmt.Fprintln(diag, err)
		return 1
	}
	if *debug || *logLevel == "debug" {
		fmt.Fprintf(diag, "rendered %d bytes\n", len(result))
	}
	if *dryRun {
		return 0
	}
	if command == "helm2-chart" {
		if *outputDir == "" {
			fmt.Fprintln(diag, "--output-dir is required for helm2-chart")
			return 2
		}
		if err := packageHelm2Chart(result, *outputDir); err != nil {
			fmt.Fprintln(diag, err)
			return 1
		}
		return 0
	}
	if _, err = out.Write(result); err != nil {
		fmt.Fprintln(diag, err)
		return 1
	}
	return 0
}

func packageHelm2Chart(manifests []byte, outputDir string) error {
	parent, err := os.OpenRoot(filepath.Dir(outputDir))
	if err != nil {
		return fmt.Errorf("open Helm 2 chart parent: %w", err)
	}
	defer parent.Close()
	name := filepath.Base(outputDir)
	if err := parent.Mkdir(name, 0700); err != nil {
		return fmt.Errorf("create Helm 2 chart: %w", err)
	}
	output, err := parent.OpenRoot(name)
	if err != nil {
		return err
	}
	defer output.Close()
	for _, sub := range []string{"files", "templates"} {
		if err = output.Mkdir(sub, 0700); err != nil {
			return err
		}
	}
	files := map[string][]byte{
		"Chart.yaml":              []byte("apiVersion: v1\nname: vault-rendered\nversion: 0.1.0\n"),
		"files/rendered.yaml":     manifests,
		"templates/rendered.yaml": []byte("{{ .Files.Get \"files/rendered.yaml\" }}\n"),
	}
	for name, data := range files {
		if err = output.WriteFile(name, data, 0600); err != nil {
			return err
		}
	}
	return nil
}
