package cmd

import (
	"encoding/json"
	"fmt"
	"io"
	"runtime"
	"strings"

	cl "bitbucket.org/ai69/colorlogo"
	"github.com/1set/gut/yos"
	"github.com/1set/gut/ystring"
	"github.com/b1ug/gid"
	"github.com/b1ug/nb1/config"
	"github.com/b1ug/nb1/internal/doctor"
	"github.com/spf13/cobra"
)

var colorLogo = cl.RoseWaterByLine(config.AppLogoArt)

// versionFields retains the established human fields, including explicitly
// supplied linker values. The JSON envelope uses only the checked doctor.Build.
type versionFields struct {
	name, buildNum, buildDate, buildHost        string
	goVersion, gitBranch, gitCommit, gitSummary string
}

type versionMetadata struct {
	SchemaVersion int          `json:"schema_version"`
	Name          string       `json:"name"`
	Version       string       `json:"version"`
	Build         doctor.Build `json:"build"`
}

func resolvedVersionFields(fields versionFields, build doctor.Build) versionFields {
	if strings.TrimSpace(fields.name) == "" {
		fields.name = "nb1"
	}
	if strings.TrimSpace(fields.goVersion) == "" {
		fields.goVersion = build.GoVersion
		if fields.goVersion == "" {
			fields.goVersion = runtime.Version()
		}
	}
	dirty := ""
	if build.Modified == "true" {
		dirty = "-dirty"
	}
	if strings.TrimSpace(fields.gitCommit) == "" && build.Revision != "" {
		fields.gitCommit = build.Revision + dirty
	}
	if strings.TrimSpace(fields.gitSummary) == "" {
		switch {
		case build.Revision != "":
			revision := build.Revision
			if len(revision) > 12 {
				revision = revision[:12]
			}
			fields.gitSummary = "candidate-" + revision + dirty
		case build.Main.Version != "" && build.Main.Version != "(devel)":
			fields.gitSummary = build.Main.Version
			if dirty != "" && !strings.HasSuffix(fields.gitSummary, "-dirty") && !strings.HasSuffix(fields.gitSummary, "+dirty") {
				fields.gitSummary += dirty
			}
		default:
			fields.gitSummary = "development"
		}
	}
	return fields
}

func writeVersion(output io.Writer, fields versionFields, build doctor.Build, asJSON bool) error {
	fields = resolvedVersionFields(fields, build)
	if asJSON {
		encoder := json.NewEncoder(output)
		encoder.SetIndent("", "  ")
		return encoder.Encode(versionMetadata{SchemaVersion: 1, Name: fields.name, Version: fields.gitSummary, Build: build})
	}

	var text strings.Builder
	text.WriteString(colorLogo)
	text.WriteString(ystring.NewLine)
	arrow := "➣ "
	if yos.IsOnWindows() {
		arrow = "> "
	}
	addNonBlankField := func(name, value string) {
		if ystring.IsNotBlank(value) {
			fmt.Fprintln(&text, arrow+name+":", value)
		}
	}
	addNonBlankField("App Name  ", fields.name)
	addNonBlankField("Build Num ", fields.buildNum)
	addNonBlankField("Build Date", fields.buildDate)
	addNonBlankField("Build Host", fields.buildHost)
	addNonBlankField("Go Version", fields.goVersion)
	addNonBlankField("Git Branch", fields.gitBranch)
	addNonBlankField("Git Commit", fields.gitCommit)
	addNonBlankField("GitSummary", fields.gitSummary)
	_, err := fmt.Fprintln(output, text.String())
	return err
}

func newVersionCommand(readBuild func() doctor.Build) *cobra.Command {
	var asJSON bool
	command := &cobra.Command{
		Use:     "version",
		Aliases: aliasesVersion,
		Short:   "Show version information",
		Long:    "Print this executable's version and build information without contacting a daemon or USB device.",
		Example: hdocf(`
		# show version
		$ %[1]s version
		# machine-readable build information
		$ %[1]s version --json
	`, config.AppName),
		Args: cobra.NoArgs,
		// Build diagnostics must not depend on a valid runtime configuration.
		PersistentPreRunE: func(*cobra.Command, []string) error { return nil },
		RunE: func(command *cobra.Command, _ []string) error {
			fields := versionFields{
				name: config.AppName, buildNum: config.CIBuildNum,
				buildDate: config.BuildDate, buildHost: config.BuildHost,
				goVersion: config.GoVersion, gitBranch: config.GitBranch,
				gitCommit: config.GitCommit, gitSummary: config.GitSummary,
			}
			return writeVersion(command.OutOrStdout(), fields, readBuild(), asJSON)
		},
	}
	command.Flags().BoolVar(&asJSON, "json", false, "print schema_version=1 build metadata without the logo or ANSI styling")
	return command
}

var versionCmd = newVersionCommand(func() doctor.Build {
	// Supported is a compiled backend check. ReadBuild reads executable metadata;
	// neither function enumerates USB or contacts the local daemon.
	return doctor.ReadBuild(gid.Supported())
})

func init() { rootCmd.AddCommand(versionCmd) }
