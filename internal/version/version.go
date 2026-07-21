// Package version holds build metadata injected via -ldflags by GoReleaser.
package version

var (
	Version = "dev"
	Commit  = "none"
)

func String() string {
	return Version + " (" + Commit + ")"
}
