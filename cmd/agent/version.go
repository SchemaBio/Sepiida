package main

import (
	"fmt"
	"runtime"
	"runtime/debug"
)

func agentVersion() string {
	revision, modified := "unknown", "unknown"
	if info, ok := debug.ReadBuildInfo(); ok {
		for _, setting := range info.Settings {
			switch setting.Key {
			case "vcs.revision":
				revision = setting.Value
			case "vcs.modified":
				modified = setting.Value
			}
		}
	}
	return fmt.Sprintf("sepiida-agent revision=%s modified=%s %s %s/%s", revision, modified, runtime.Version(), runtime.GOOS, runtime.GOARCH)
}
