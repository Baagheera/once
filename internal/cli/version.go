package cli

import (
	"fmt"
	"io"
	"runtime/debug"
)

var version = "dev"

func versionCommand(args []string, stdout, stderr io.Writer) int {
	if len(args) != 0 {
		fmt.Fprintln(stderr, "once: version does not take positional arguments")
		return 2
	}
	fmt.Fprintf(stdout, "once %s\n", runtimeVersion())
	return 0
}

func runtimeVersion() string {
	if version != "" && version != "dev" {
		return version
	}
	if info, ok := debug.ReadBuildInfo(); ok {
		if info.Main.Version != "" && info.Main.Version != "(devel)" {
			return info.Main.Version
		}
	}
	if version == "" {
		return "dev"
	}
	return version
}
