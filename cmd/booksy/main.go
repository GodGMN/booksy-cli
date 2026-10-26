package main

import (
	"os"
	"runtime/debug"

	"github.com/GodGMN/booksy-cli/internal/cli"
)

// version is set by -ldflags "-X main.version=..." in release builds.
var version = ""

func main() {
	os.Exit(cli.Execute(resolveVersion(), os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}

func resolveVersion() string {
	if version != "" {
		return version
	}
	if bi, ok := debug.ReadBuildInfo(); ok && bi.Main.Version != "" && bi.Main.Version != "(devel)" {
		return bi.Main.Version
	}
	return "dev"
}
