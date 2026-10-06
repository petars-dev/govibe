// Package config loads the fixture's settings from env.yml.
package config

// Config mirrors env.yml. The yaml tags are what the indexer maps usage onto.
type Config struct {
	Env    string `yaml:"env"`
	Server struct {
		Port string `yaml:"port"`
	} `yaml:"server"`
	Database struct {
		DSN      string `yaml:"dsn"`
		MaxConns int    `yaml:"max_conns"`
	} `yaml:"database"`
	Auth struct {
		APIKey string `yaml:"api_key"`
	} `yaml:"auth"`
}

// Load returns the settings. The fixture is never run, so this is a stub.
func Load() Config { return Config{} }

// Get returns a setting by dotted path, e.g. Get("mail.smtp.host").
func Get(path string) (string, bool) { return "", false }
