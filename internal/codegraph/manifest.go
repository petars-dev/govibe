package codegraph

// Manifest is what the framework publishes about itself, so that a project's graph can
// name the framework's side of every connection without indexing the framework: its
// settings (so a read of `server.port` is not "read but not declared", and the app is
// not told it never reads `dev.graph.calls`) and its MCP tools. Whatever it contributes
// carries the origin `framework`, so the app-only view hides it and scope `all` labels it.
//
// The framework's routes and middleware are not here: the running router reports them
// with who registered them, and the offline run reads them from `inspect --json`. Its
// public API appears as the external nodes the project's imports and calls point at.
type Manifest struct {
	Module  string
	Version string

	// ConfigYAML is the framework's defaults file as it ships (the keys, with their
	// ${ENV:default} placeholders): what an app's env.yml is merged over.
	ConfigYAML []byte
	// ConfigFields maps the Go fields of the framework's typed Config to their keys:
	// "Server.Port" is "server.port". With it `cfg.Server.Port` is a read of that key.
	ConfigFields map[string]string

	Tools []ManifestTool
}

// ManifestTool is an MCP tool the framework serves.
type ManifestTool struct {
	Name        string
	Description string
}
