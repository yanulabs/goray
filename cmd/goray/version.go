package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"runtime"
	"runtime/debug"
	"strings"
)

var (
	version   = "dev"
	commit    = ""
	buildDate = ""
)

type versionOptions struct {
	short bool
	json  bool
}

type versionInfo struct {
	Name     string `json:"name"`
	Version  string `json:"version"`
	Commit   string `json:"commit,omitempty"`
	Built    string `json:"built,omitempty"`
	Go       string `json:"go"`
	Platform string `json:"platform"`
}

func buildVersion() string {
	if version != "" && version != "dev" {
		return version
	}
	if info, ok := debug.ReadBuildInfo(); ok && info.Main.Version != "" && info.Main.Version != "(devel)" {
		return info.Main.Version
	}
	return version
}

func currentVersionInfo() versionInfo {
	info := versionInfo{
		Name:     "goray",
		Version:  buildVersion(),
		Commit:   strings.TrimSpace(commit),
		Built:    strings.TrimSpace(buildDate),
		Go:       runtime.Version(),
		Platform: runtime.GOOS + "/" + runtime.GOARCH,
	}
	// Local builds normally carry VCS metadata in debug.ReadBuildInfo. Use it
	// when Makefile/GoReleaser did not inject explicit values.
	if build, ok := debug.ReadBuildInfo(); ok {
		if info.Commit == "" {
			info.Commit = buildSetting(build, "vcs.revision")
		}
		if info.Built == "" {
			info.Built = buildSetting(build, "vcs.time")
		}
	}
	return info
}

func buildSetting(info *debug.BuildInfo, key string) string {
	for _, setting := range info.Settings {
		if setting.Key == key {
			return strings.TrimSpace(setting.Value)
		}
	}
	return ""
}

func parseVersionArgs(args []string) (versionOptions, error) {
	var opts versionOptions
	fs := flag.NewFlagSet("version", flag.ContinueOnError)
	quietFlagSet(fs)
	fs.BoolVar(&opts.short, "short", false, "print only the version")
	fs.BoolVar(&opts.json, "json", false, "print version metadata as JSON")
	positionals, err := parseFlagSet(fs, args)
	if err != nil {
		return versionOptions{}, err
	}
	if len(positionals) > 0 {
		return versionOptions{}, usagef("version takes no arguments")
	}
	if opts.short && opts.json {
		return versionOptions{}, usagef("version --short and --json are mutually exclusive")
	}
	return opts, nil
}

func printVersion(w io.Writer, opts versionOptions) {
	info := currentVersionInfo()
	if opts.short {
		_, _ = fmt.Fprintln(w, info.Version)
		return
	}
	if opts.json {
		data, err := json.Marshal(info)
		if err == nil {
			_, _ = fmt.Fprintln(w, string(data))
		}
		return
	}

	// Keep the original first line stable for scripts that already parse
	// `goray <version>`, then add the details normally shown by Go/Docker-style
	// version commands.
	_, _ = fmt.Fprintf(w, "%s %s\n", info.Name, info.Version)
	if info.Commit != "" {
		_, _ = fmt.Fprintf(w, "commit: %s\n", shortCommit(info.Commit))
	}
	if info.Built != "" {
		_, _ = fmt.Fprintf(w, "built: %s\n", info.Built)
	}
	_, _ = fmt.Fprintf(w, "go: %s %s\n", info.Go, info.Platform)
}

func shortCommit(value string) string {
	value = strings.TrimSpace(value)
	if len(value) > 7 {
		return value[:7]
	}
	return value
}
