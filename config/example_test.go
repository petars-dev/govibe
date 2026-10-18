package config_test

import (
	"fmt"
	"testing/fstest"

	"github.com/petars-dev/govibe/config"
)

// An app's env.yml is layered over the framework's defaults, and ${ENV:default}
// placeholders are resolved from the process environment. The framework's keys are typed
// fields; the app's own are read with String, Get and Unmarshal.
func ExampleLoad() {
	envYML := `
shop:
  currency: ${SHOP_CURRENCY:EUR}
  checkout:
    max_items: 20
`
	cfg, err := config.Load(fstest.MapFS{"env.yml": {Data: []byte(envYML)}})
	if err != nil {
		panic(err)
	}
	var shop struct {
		Currency string `yaml:"currency"`
		Checkout struct {
			MaxItems int `yaml:"max_items"`
		} `yaml:"checkout"`
	}
	if err := cfg.Unmarshal("shop", &shop); err != nil {
		panic(err)
	}
	fmt.Println(cfg.String("shop.currency"), shop.Checkout.MaxItems, cfg.Server.Port)
	// Output: EUR 20 8080
}
