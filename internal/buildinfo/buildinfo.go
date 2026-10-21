// Package buildinfo resolves the version both binaries print. It imports
// only runtime/debug, so cmgo-hook can use it without growing its start-up.
package buildinfo

import "runtime/debug"

// Version is ldflag when the build set one (-ldflags "-X main.version=..."),
// else the main module version a `go install module@version` records, else
// "dev". A source-tree build records "(devel)", which also means dev.
func Version(ldflag string) string { return resolve(ldflag, debug.ReadBuildInfo) }

func resolve(ldflag string, info func() (*debug.BuildInfo, bool)) string {
	if ldflag != "" {
		return ldflag
	}
	if bi, ok := info(); ok && bi.Main.Version != "" && bi.Main.Version != "(devel)" {
		return bi.Main.Version
	}
	return "dev"
}
