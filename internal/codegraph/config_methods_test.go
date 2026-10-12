package codegraph

import (
	"context"
	"testing"
)

// The dynamic helpers are methods of the loaded config (rt.Config.Get, cfg.Unmarshal):
// reading a key that way is a use, so the key is not "declared but never read".
func TestConfigMethodReadsAreUses(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "go.mod", "module example.com/mini\n\ngo 1.25\n")
	writeFile(t, root, "env.yml", `env: ${APP_ENV:local}
shop:
  currency: ${SHOP_CURRENCY:EUR}
  limits:
    max: 5
    min: 1
  other: x
unused:
  key: never read
`)
	writeFile(t, root, "config/config.go", `package config

// Config is the loaded configuration.
type Config struct {
	Env string `+"`yaml:\"env\"`"+`
}

// Get returns the value at a dotted path.
func (c Config) Get(path string) (any, bool) { return nil, false }

// Unmarshal decodes the subtree at a dotted path.
func (c Config) Unmarshal(path string, v any) error { return nil }
`)
	writeFile(t, root, "shop/shop.go", `package shop

import "example.com/mini/config"

// Runtime carries the configuration.
type Runtime struct{ Config config.Config }

// Currency reads one key through the runtime.
func Currency(rt *Runtime) any {
	v, _ := rt.Config.Get("shop.currency")
	return v
}

// Limits reads a whole group through a local config value.
func Limits(cfg config.Config) error {
	var l struct{ Max, Min int }
	return cfg.Unmarshal("shop.limits", &l)
}
`)

	h, _ := openStore(t)
	if _, err := New(h.QuadStore, Options{Root: root}).Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	m, err := Load(context.Background(), h.QuadStore)
	if err != nil {
		t.Fatal(err)
	}

	unused := map[string]bool{}
	for _, id := range byRule(m)["config.unused"] {
		unused[id] = true
	}
	if !unused["finding:config.unused:cfg:unused.key"] || !unused["finding:config.unused:cfg:shop.other"] {
		t.Errorf("keys nothing reads must still be reported: %v", unused)
	}
	for _, key := range []string{"shop.currency", "shop.limits.max", "shop.limits.min"} {
		if unused["finding:config.unused:cfg:"+key] {
			t.Errorf("%s is read through a config method and must not be reported unused", key)
		}
	}
	f := facts{storedLabel(t, h, "config")}
	for _, want := range [][2]string{
		{"func:shop.Currency", "cfg:shop.currency"},
		{"func:shop.Limits", "cfg:shop.limits.max"},
		{"func:shop.Limits", "cfg:shop.limits.min"},
		{"pkg:shop", "cfg:shop.currency"},
	} {
		if !f.has(want[0], "uses_config", iri(want[1])) {
			t.Errorf("missing %s uses_config %s", want[0], want[1])
		}
	}
}
