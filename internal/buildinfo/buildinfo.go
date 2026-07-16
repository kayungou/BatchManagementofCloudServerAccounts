package buildinfo

// These values are replaced with -ldflags by release builds.
var (
	Version   = "dev"
	Commit    = "unknown"
	BuildTime = "unknown"
)
