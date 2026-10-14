module example.com/fixture

go 1.25.0

require example.com/stubfw v0.0.0

// In a workspace the stub is a member (go.work); the replace makes the module resolve the same
// way outside one, and when the workspace graph is loaded.
replace example.com/stubfw => ../stubfw
