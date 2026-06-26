// Package config loads symingest configuration.
package config

import (
	"github.com/danieljustus/symaira-corekit/configkit"
)

// Config holds symingest configuration.
// Values are loaded from ~/.config/symingest/config.toml, ./.symingest.toml,
// and environment variables prefixed with SYMINGEST_.
type Config struct {
	Vault       string `json:"vault"`
	OCRLang     string `json:"ocr_lang"`
	DBPath      string `json:"db_path"`
	ArchivePath string `json:"archive_path"`
}

// Defaults returns the default configuration.
func Defaults() *Config {
	return &Config{
		Vault:       "",
		OCRLang:     "eng",
		DBPath:      "",
		ArchivePath: "",
	}
}

// Loader is the application-wide config loader.
var Loader = configkit.NewLoader(configkit.Options{
	AppName:    "symingest",
	EnvPrefix:  "SYMINGEST",
	ConfigName: "symingest",
}, Defaults)

// Load returns the loaded configuration.
func Load() (*Config, error) {
	return Loader.Load()
}
