// SPDX-License-Identifier: Apache-2.0

// vayu-compat — validate a VCB extension package (plugin.json / theme.json)
// against the VayuPress compatibility contract (the Vayu Compatibility Bible,
// ADR-0135). It runs the exact same checks the host applies, so an extension
// that passes here installs and runs without compatibility surprises.
//
// Usage:
//
//	vayu-compat check --manifest ./myplugin/plugin.json [--host 3.13.41] [--files]
//	vayu-compat check --manifest ./mytheme/theme.json  [--host 3.13.41]
//	vayu-compat hooks
//	vayu-compat capabilities
//
// Exits with code 1 if any ERROR-severity finding is reported.
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/johalputt/vayupress/internal/vcb"
	"github.com/johalputt/vayupress/internal/vcb/validate"
)

func main() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

var (
	flagManifest string
	flagHost     string
	flagFiles    bool
)

var rootCmd = &cobra.Command{
	Use:   "vayu-compat",
	Short: "Validate VCB extension manifests against the VayuPress compatibility contract",
}

var checkCmd = &cobra.Command{
	Use:   "check",
	Short: "Validate a plugin.json or theme.json (kind auto-detected)",
	RunE:  runCheck,
}

var hooksCmd = &cobra.Command{
	Use:   "hooks",
	Short: "Print the enumerated plugin-hook catalogue",
	RunE:  runHooks,
}

var capsCmd = &cobra.Command{
	Use:   "capabilities",
	Short: "Print the section:action capability vocabulary",
	RunE:  runCapabilities,
}

func init() {
	checkCmd.Flags().StringVar(&flagManifest, "manifest", vcb.ManifestFilename, "Path to plugin.json or theme.json")
	checkCmd.Flags().StringVar(&flagHost, "host", "", "Host VayuPress version to check compatibility against (e.g. 3.13.41)")
	checkCmd.Flags().BoolVar(&flagFiles, "files", false, "Also verify the plugin executable exists and matches executable_sha256")
	rootCmd.AddCommand(checkCmd, hooksCmd, capsCmd)
}

func runCheck(cmd *cobra.Command, _ []string) error {
	opts := validate.Options{
		HostVersion: flagHost,
		BaseDir:     filepath.Dir(flagManifest),
		CheckFiles:  flagFiles,
	}

	// Kind-named files go through the bounded loaders (256 KiB cap, strict
	// parsing); anything else is read and shape-sniffed by checkManifest.
	var res *validate.Result
	switch strings.ToLower(filepath.Base(flagManifest)) {
	case vcb.ThemeManifestFilename:
		m, err := vcb.LoadThemeManifest(flagManifest)
		if err != nil {
			return err
		}
		res = validate.Theme(m, opts)
	case vcb.ManifestFilename:
		m, err := vcb.LoadPluginManifest(flagManifest)
		if err != nil {
			return err
		}
		res = validate.Plugin(m, opts)
	default:
		raw, err := os.ReadFile(flagManifest)
		if err != nil {
			return fmt.Errorf("read manifest: %w", err)
		}
		var kindErr error
		res, kindErr = checkManifest(raw, filepath.Base(flagManifest), opts)
		if kindErr != nil {
			return kindErr
		}
	}

	errors, warns := 0, 0
	for _, f := range res.Findings {
		switch f.Severity {
		case validate.Error:
			errors++
			fmt.Printf("✗ [%s] %s: %s\n", f.Code, f.Field, f.Message)
		case validate.Warn:
			warns++
			fmt.Printf("⚠ [%s] %s: %s\n", f.Code, f.Field, f.Message)
		}
	}

	fmt.Println("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
	name := res.Name
	if name == "" {
		name = "(unnamed)"
	}
	fmt.Printf("%s %q — %d error(s), %d warning(s)\n", res.Kind, name, errors, warns)
	if errors > 0 {
		fmt.Println("✗ NOT compatible — fix the errors above")
		os.Exit(1)
	}
	fmt.Println("✓ compatible with the VCB contract")
	return nil
}

// checkManifest picks the manifest kind by filename, falling back to shape
// sniffing (a theme manifest carries "tokens"; a plugin carries "executable").
func checkManifest(raw []byte, base string, opts validate.Options) (*validate.Result, error) {
	lower := strings.ToLower(base)
	switch {
	case lower == vcb.ThemeManifestFilename || strings.HasPrefix(lower, "theme"):
		m, err := vcb.ParseThemeManifest(raw)
		if err != nil {
			return nil, err
		}
		return validate.Theme(m, opts), nil
	case lower == vcb.ManifestFilename || strings.HasPrefix(lower, "plugin"):
		m, err := vcb.ParsePluginManifest(raw)
		if err != nil {
			return nil, err
		}
		return validate.Plugin(m, opts), nil
	}
	// Shape sniff: decode loosely, look at the top-level keys.
	var probe map[string]json.RawMessage
	if err := json.Unmarshal(raw, &probe); err != nil {
		return nil, fmt.Errorf("parse manifest: %w", err)
	}
	if _, ok := probe["tokens"]; ok {
		m, err := vcb.ParseThemeManifest(raw)
		if err != nil {
			return nil, err
		}
		return validate.Theme(m, opts), nil
	}
	m, err := vcb.ParsePluginManifest(raw)
	if err != nil {
		return nil, err
	}
	return validate.Plugin(m, opts), nil
}

func runHooks(cmd *cobra.Command, _ []string) error {
	fmt.Println("Plugin hooks (plugin.json \"hooks\" may list exactly these):")
	for _, h := range vcb.AllHooks {
		fmt.Printf("• %-16s %s (payload: %s)\n", h.Name, h.Description, strings.Join(h.PayloadKeys, ", "))
	}
	fmt.Println("\nOutbound webhook events (a webhook subscription may target these, or \"*\"):")
	for _, e := range vcb.WebhookEvents {
		fmt.Printf("• %s\n", e)
	}
	return nil
}

func runCapabilities(cmd *cobra.Command, _ []string) error {
	fmt.Println("Capability vocabulary (declare as \"section:action\" in api_permissions):")
	fmt.Printf("sections: ")
	for i, s := range vcb.AllSections {
		if i > 0 {
			fmt.Print(", ")
		}
		fmt.Print(string(s))
	}
	fmt.Printf("\nactions:  ")
	for i, a := range vcb.AllActions {
		if i > 0 {
			fmt.Print(", ")
		}
		fmt.Print(string(a))
	}
	fmt.Println("\n\nWildcards (\"section:*\", \"*:*\") are refused in extension manifests — declare least privilege.")
	return nil
}
