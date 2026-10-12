// Package config loads the layered configuration: the framework's defaults
// (defaults.yml, embedded here), then the application's env.yml, with
// `${ENV:default}` placeholders resolved from the process environment.
package config

import (
	_ "embed"
	"fmt"
	"io/fs"
	"os"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

//go:embed defaults.yml
var defaultsYAML []byte

// File is the name of the application's settings file inside the fs.FS handed to Load.
const File = "env.yml"

// Config is the loaded configuration: the framework's typed keys, plus Get and
// Unmarshal for the application's own. It is a value; nothing is global, so two
// loads never interfere.
type Config struct {
	Env string `yaml:"env"`
	// Root is the configured application root, "" when none is set.
	Root   string `yaml:"root"`
	Server struct {
		Port     string `yaml:"port"`
		LogLevel string `yaml:"log_level"`
	} `yaml:"server"`
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

	// raw is the resolved tree, for the dynamic access helpers. It is never
	// modified after Load, so copies of a Config may share it.
	raw map[string]any
}

// Load merges the framework defaults with the env.yml held by app and resolves
// the result from the process environment. app is the application's embedded
// file system (its root holds env.yml), or nil for the defaults alone.
//
// Layers: the application's file wins over a default of the same key; maps merge
// deeply, lists are replaced, unknown keys are allowed.
func Load(app fs.FS) (Config, error) {
	base, err := parse("defaults.yml", defaultsYAML)
	if err != nil {
		return Config{}, err
	}
	if app != nil {
		b, err := fs.ReadFile(app, File)
		if err != nil {
			return Config{}, fmt.Errorf("read app %s: %w", File, err)
		}
		over, err := parse(File, b)
		if err != nil {
			return Config{}, err
		}
		base = merge(base, over)
	}
	return fromTree(base)
}

// fromTree types a resolved tree.
func fromTree(tree map[string]any) (Config, error) {
	b, err := yaml.Marshal(tree)
	if err != nil {
		return Config{}, err
	}
	cfg := Config{raw: tree}
	if err := yaml.Unmarshal(b, &cfg); err != nil { // fills the exported fields, leaves raw alone
		return Config{}, fmt.Errorf("config: %w", err)
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

// parse reads one YAML document into a resolved tree. An empty document is an
// empty tree.
func parse(name string, b []byte) (map[string]any, error) {
	var doc yaml.Node
	if err := yaml.Unmarshal(b, &doc); err != nil {
		return nil, fmt.Errorf("parse %s: %w", name, err)
	}
	if doc.Kind == 0 { // no document at all, or only comments
		return map[string]any{}, nil
	}
	if doc.Kind != yaml.DocumentNode || len(doc.Content) != 1 {
		return nil, fmt.Errorf("parse %s: not a single YAML document", name)
	}
	top := doc.Content[0]
	if top.Kind == yaml.ScalarNode && top.Tag == "!!null" {
		return map[string]any{}, nil
	}
	if top.Kind != yaml.MappingNode {
		return nil, fmt.Errorf("parse %s: the top level must be a mapping", name)
	}
	v, err := resolveNode(top)
	if err != nil {
		return nil, fmt.Errorf("parse %s: %w", name, err)
	}
	m, _ := v.(map[string]any)
	return m, nil
}

// resolveNode turns a YAML node into plain maps, slices and scalars, replacing a
// string of the form ${VAR} or ${VAR:default} with the environment variable or
// the default.
func resolveNode(n *yaml.Node) (any, error) {
	switch n.Kind {
	case yaml.AliasNode:
		return resolveNode(n.Alias)
	case yaml.MappingNode:
		out := make(map[string]any, len(n.Content)/2)
		for i := 0; i+1 < len(n.Content); i += 2 {
			v, err := resolveNode(n.Content[i+1])
			if err != nil {
				return nil, err
			}
			out[n.Content[i].Value] = v
		}
		return out, nil
	case yaml.SequenceNode:
		out := make([]any, 0, len(n.Content))
		for _, c := range n.Content {
			v, err := resolveNode(c)
			if err != nil {
				return nil, err
			}
			out = append(out, v)
		}
		return out, nil
	case yaml.ScalarNode:
		if n.Tag == "!!str" && isPlaceholder(n.Value) {
			quoted := n.Style&(yaml.DoubleQuotedStyle|yaml.SingleQuotedStyle) != 0
			return expandEnvLike(n.Value, quoted), nil
		}
		var v any
		if err := n.Decode(&v); err != nil {
			return nil, err
		}
		return v, nil
	}
	return nil, nil
}

func isPlaceholder(s string) bool {
	return strings.HasPrefix(s, "${") && strings.HasSuffix(s, "}")
}

// expandEnvLike resolves one placeholder. The value is typed the way YAML would
// type it (bool, integer, float, else string), so `${PORT:8080}` and
// `${DEBUG:false}` bind to int and bool fields; a quoted placeholder keeps its
// value a string, which is how a version like `${V:1.10}` or a code like
// `${ZIP:00123}` stays what it says.
func expandEnvLike(s string, quoted bool) any {
	inner := strings.TrimSuffix(strings.TrimPrefix(s, "${"), "}")
	key, def, _ := strings.Cut(inner, ":")
	val, ok := os.LookupEnv(key)
	if !ok || val == "" {
		val = def
	}
	if quoted {
		return val
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

// merge deep-merges over into base: maps merge, everything else is replaced.
func merge(base, over map[string]any) map[string]any {
	for k, v := range over {
		if bm, ok := base[k].(map[string]any); ok {
			if om, ok := v.(map[string]any); ok {
				base[k] = merge(bm, om)
				continue
			}
		}
		base[k] = v
	}
	return base
}

// Get returns the value at a dotted path, if present. E.g. Get("shop.currency").
func (c Config) Get(path string) (any, bool) {
	if c.raw == nil {
		return nil, false
	}
	var cur any = c.raw
	for _, p := range strings.Split(path, ".") {
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

// String returns the value at a dotted path as text, "" when absent.
func (c Config) String(path string) string {
	v, ok := c.Get(path)
	if !ok || v == nil {
		return ""
	}
	return fmt.Sprint(v)
}

// Unmarshal decodes the subtree at a dotted path into v.
func (c Config) Unmarshal(path string, v any) error {
	val, ok := c.Get(path)
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
