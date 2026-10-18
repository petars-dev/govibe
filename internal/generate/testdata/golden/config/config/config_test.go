package config

import (
	"testing"
	"testing/fstest"

	govibeconfig "github.com/petars-dev/govibe/config"
)

func TestLoadReadsTheSection(t *testing.T) {
	env := fstest.MapFS{"env.yml": &fstest.MapFile{Data: []byte("shop:\n  greeting: x\n  page_size: 7\n  smtp:\n    host: x\n    tls: true\n")}}
	cfg, err := govibeconfig.Load(env)
	if err != nil {
		t.Fatal(err)
	}
	got, err := Load(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if got.Greeting != "x" {
		t.Errorf("Load = %+v", got)
	}
}
