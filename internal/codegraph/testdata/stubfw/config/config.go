// Package config is the framework's layered configuration.
package config

// Config is the loaded configuration.
type Config struct {
	Env string `yaml:"env"`
}

// Get returns a setting by dotted path.
func (c Config) Get(key string) (string, bool) { return "", false }
