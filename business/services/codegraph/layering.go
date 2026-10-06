package codegraph

// The layering rule: which architecture layer (a package's `arch_layer`) may
// import which. It is SKILL.md's "Key Constraints" written as data, so the
// findings layer and the architecture overview cannot disagree about it:
//
//   - only business actions and services may call the storage layer;
//   - storage is the lowest layer: it depends on nothing above it;
//   - business logic does not depend on the HTTP layer.
//
// `wiring` (routes, main, cmd) and `dev` (devmcp) packages are the allow-list:
// they assemble the application and may import anything. Everything else is
// checked, including a controller that is only used by dev pages: a controller
// that touches storage is the violation the rule exists to find.

// layerVerdict explains why a package of architecture layer `from` must not
// import one of layer `to`; it returns "" when the import is allowed.
func layerVerdict(from, to string) string {
	if from == to || from == "" || to == "" || from == "wiring" || from == "dev" {
		return ""
	}
	switch from {
	case "storage":
		switch to {
		case "support":
			return ""
		}
		return "storage is the lowest layer: it must not depend on " + to + " packages"
	case "business":
		switch to {
		case "controllers", "middleware", "views", "wiring", "dev":
			return "business logic must not depend on " + to + " packages (controllers call business, not the other way round)"
		}
	}
	if to == "storage" && from != "business" {
		// controllers, middleware, views, support: nothing but business may call storage.
		return "only business actions and services may call storage; " + from + " packages should go through them"
	}
	return ""
}
