package codegraph

import "github.com/petars-dev/govibe/internal/rules"

// layerVerdict explains why a package of architecture layer `from` must not import one
// of layer `to`; it returns "" when the import is allowed. The rule itself is data of
// internal/rules, so the findings, the layer report and the SKILL.md text cannot
// disagree about it.
func layerVerdict(from, to string) string { return rules.LayerVerdict(from, to) }
