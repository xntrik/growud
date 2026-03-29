package main

import (
	"os"
	"path/filepath"
	"strings"
)

// AppPaths holds resolved paths for config, database, and cache.
type AppPaths struct {
	DataDir     string // config.env + growud.db
	CacheDir    string // API cache
	DBPath      string // growud.db full path
	ConfigEnv   string // config.env full path
	ReadingsDir string // raw JSON archive
	TariffPath  string // tariff.json full path
	LogDir      string // log files (bundle mode only)
	LogPath     string // growud.log full path
}

// isAppBundle returns true if the binary is running inside a .app bundle.
func isAppBundle() bool {
	exe, err := os.Executable()
	if err != nil {
		return false
	}
	return strings.Contains(exe, ".app/Contents/MacOS/")
}

// resolveAppPaths returns appropriate paths based on whether we're running
// as a .app bundle or as a CLI tool.
func resolveAppPaths() AppPaths {
	if isAppBundle() {
		return bundlePaths()
	}
	return cliPaths()
}

// cliPaths returns paths relative to the current working directory.
// This preserves existing CLI behaviour.
func cliPaths() AppPaths {
	return AppPaths{
		DataDir:     ".",
		CacheDir:    ".cache",
		DBPath:      "growud.db",
		ConfigEnv:   ".env",
		ReadingsDir: ".cache/readings",
		TariffPath:  "tariff.json",
	}
}

// bundlePaths returns standard macOS paths for a bundled .app.
func bundlePaths() AppPaths {
	home, _ := os.UserHomeDir()
	dataDir := filepath.Join(home, "Library", "Application Support", "Growud")
	cacheDir := filepath.Join(home, "Library", "Caches", "Growud")
	logDir := filepath.Join(home, "Library", "Logs", "Growud")

	os.MkdirAll(dataDir, 0755)
	os.MkdirAll(cacheDir, 0755)
	os.MkdirAll(logDir, 0755)

	return AppPaths{
		DataDir:     dataDir,
		CacheDir:    cacheDir,
		DBPath:      filepath.Join(dataDir, "growud.db"),
		ConfigEnv:   filepath.Join(dataDir, "config.env"),
		ReadingsDir: filepath.Join(cacheDir, "readings"),
		TariffPath:  filepath.Join(dataDir, "tariff.json"),
		LogDir:      logDir,
		LogPath:     filepath.Join(logDir, "growud.log"),
	}
}
