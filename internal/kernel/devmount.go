package kernel

// mountDev hands the finished router to the dev tools. It is nil in a default
// build: devmount_dev.go sets it, and only a build with the govibe_dev tag compiles
// that file, so a default build links none of the dev stack (the graph store, the
// indexer, the MCP server, the database explorer, the panel's files) and the
// binary cannot serve /dev by accident. This file and that one are the only seam;
// nothing else in the kernel names the dev tools.
var mountDev func(b *Built)
