// Package version reports the build version of the bot.
package version

// version is overridden at build time via -ldflags; dev default below.
var version = "dev"

// String returns the current build version.
func String() string { return version }
