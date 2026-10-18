package config

import (
	"testing"
	"testing/fstest"

	govibeconfig "github.com/petars-dev/govibe/config"
)

func TestLoadReadsTheSection(t *testing.T) {
	env := fstest.MapFS{"env.yml": &fstest.MapFile{Data: []byte("shop:\n")}}
	cfg, err := govibeconfig.Load(env)
	if err != nil {
		t.Fatal(err)
	}
	got, err := Load(cfg)
	if err != nil {
		t.Fatal(err)
	}
	_ = got
}
