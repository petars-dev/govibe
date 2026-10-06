package config

import (
	_ "embed"
	"fmt"
	"os"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

//go:embed env.yml
var envYAML []byte

type Config struct {
	Env    string `yaml:"env"`
	Server struct {
		Port     string `yaml:"port"`
		LogLevel string `yaml:"log_level"`
	} `yaml:"server"`
	WorkOS struct {
		APIKey   string `yaml:"api_key"`
		ClientID string `yaml:"client_id"`
	} `yaml:"workos"`
	Database struct {
		// Type selects the goose migration dialect (mysql, postgres, sqlite3,
		// ... — see github.com/pressly/goose/v3 for supported values). Goose
		// otherwise silently defaults to postgres, which breaks migrations
		// against any other database, so this is always set explicitly.
		Type    string `yaml:"type"`
		MariaDB struct {
			DSN string `yaml:"dsn"`
		} `yaml:"mariadb"`
	} `yaml:"database"`
	Dev struct {
		DB struct {
			AllowMutations bool `yaml:"allow_mutations"`
		} `yaml:"db"`
		// Graph switches on the opt-in layers of the project graph.
		Graph struct {
			Calls bool `yaml:"calls"`
			Git   bool `yaml:"git"`
		} `yaml:"graph"`
	} `yaml:"dev"`
}

// Load parses the embedded env.yml, resolving values from the process environment.
func Load() (Config, error) {
	var raw map[string]any
	if err := yaml.Unmarshal(envYAML, &raw); err != nil {
		return Config{}, fmt.Errorf("parse env.yml: %w", err)
	}
	// Resolve ${ENV:default} expressions.
	resolved := resolveNode(raw)
	// Marshal back and unmarshal into typed Config.
	b, err := yaml.Marshal(resolved)
	if err != nil {
		return Config{}, err
	}
	var cfg Config
	if err := yaml.Unmarshal(b, &cfg); err != nil {
		return Config{}, err
	}
	// Keep a copy of the resolved tree for dynamic access helpers.
	if m, ok := resolved.(map[string]any); ok {
		cfgRaw = m
	} else {
		cfgRaw = map[string]any{}
	}
	// Ensure sane fallbacks.
	if cfg.Env == "" {
		cfg.Env = "local"
	}
	if cfg.Server.Port == "" {
		cfg.Server.Port = "8080"
	}
	if cfg.Server.LogLevel == "" {
		cfg.Server.LogLevel = "info"
	}
	if cfg.Database.Type == "" {
		cfg.Database.Type = "mysql"
	}
	return cfg, nil
}

// resolveNode walks a parsed YAML tree (as generic maps/slices) and replaces string values of the
// form ${VAR} or ${VAR:default} with the corresponding environment variable or default.
func resolveNode(v any) any {
	switch t := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(t))
		for k, vv := range t {
			out[k] = resolveNode(vv)
		}
		return out
	case []any:
		for i := range t {
			t[i] = resolveNode(t[i])
		}
		return t
	case string:
		return expandEnvLike(t)
	default:
		return v
	}
}

func expandEnvLike(s string) any {
	// Only replace if it looks like a single env placeholder, not a free-text string.
	if strings.HasPrefix(s, "${") && strings.HasSuffix(s, "}") {
		inner := strings.TrimSuffix(strings.TrimPrefix(s, "${"), "}")
		parts := strings.SplitN(inner, ":", 2)
		key := parts[0]
		def := ""
		if len(parts) == 2 {
			def = parts[1]
		}
		val, ok := os.LookupEnv(key)
		if !ok || val == "" {
			val = def
		}
		if b, err := strconv.ParseBool(val); err == nil {
			return b
		}
		if i, err := strconv.ParseInt(val, 10, 64); err == nil {
			return i
		}
		if f, err := strconv.ParseFloat(val, 64); err == nil {
			return f
		}
		return val
	}
	return s
}

// Backing store for dynamic access helpers (resolved YAML tree).
var cfgRaw map[string]any

// Get returns the value at a dotted path from the loaded configuration, if present.
// E.g. Get("mail.smtp.host")
func Get(path string) (any, bool) {
	if cfgRaw == nil {
		return nil, false
	}
	parts := strings.Split(path, ".")
	var cur any = cfgRaw
	for _, p := range parts {
		m, ok := cur.(map[string]any)
		if !ok {
			return nil, false
		}
		v, ok := m[p]
		if !ok {
			return nil, false
		}
		cur = v
	}
	return cur, true
}

// Unmarshal a subtree of the configuration at dotted path into v.
func Unmarshal(path string, v any) error {
	val, ok := Get(path)
	if !ok {
		return fmt.Errorf("config path not found: %s", path)
	}
	b, err := yaml.Marshal(val)
	if err != nil {
		return err
	}
	return yaml.Unmarshal(b, v)
}

// Env helpers
func (c Config) IsLocal() bool { return strings.EqualFold(c.Env, "local") }
func (c Config) IsDev() bool {
	e := strings.ToLower(c.Env)
	return e == "dev" || e == "development"
}
func (c Config) IsStaging() bool { return strings.EqualFold(c.Env, "staging") }
func (c Config) IsProd() bool {
	e := strings.ToLower(c.Env)
	return e == "prod" || e == "production"
}
