// Package config binds this application's own settings, the "shop" section of
// env.yml, to a struct. Declare a key in env.yml, add its field here (the yaml tag is
// the key) and read it through Load.
package config

import (
	govibeconfig "github.com/petars-dev/govibe/config"
)

// Config is the "shop" section of env.yml.
type Config struct {
	Greeting string `yaml:"greeting"`
	PageSize int    `yaml:"page_size"`
	Smtp     struct {
		Host string `yaml:"host"`
		Tls  bool   `yaml:"tls"`
	} `yaml:"smtp"`
}

// Load reads the section from the loaded configuration: config.Load(rt.Config). A
// section env.yml does not declare yet leaves the zero value.
func Load(cfg govibeconfig.Config) (Config, error) {
	var out Config
	if _, ok := cfg.Get("shop"); !ok {
		return out, nil
	}
	err := cfg.Unmarshal("shop", &out)
	return out, err
}
