package routes

import "example.com/stubfw"

// DevTools is what the app adds to the dev panel: its own MCP tools and pages. A
// govibe_dev build sets it (devtools_dev.go); in a default build it stays nil and the
// framework does nothing with it.
var DevTools func(rt *stubfw.Runtime, dev *stubfw.Dev) error
