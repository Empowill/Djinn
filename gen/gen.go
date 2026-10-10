// Package gen holds what `go tool task gen` generates from the protos in api/.
package gen

import _ "embed"

// Descriptors is the FileDescriptorSet of the protos in api/, with their comments and without their imports.
// The command line reads its commands and its help from it.
//
//go:embed djinn.binpb
var Descriptors []byte
