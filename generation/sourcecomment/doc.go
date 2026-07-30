// Package sourcecomment implements the adapter-neutral core of
// nexa.dev/source-comment/v1.
//
// Language adapters are responsible for parsing native syntax and binding each
// directive to a semantic node. This package parses directive lines, validates
// a closed typed registry, resolves provenance, merges fact graphs, and emits a
// deterministic canonical representation. It deliberately does not scan source
// text to guess semantic bindings.
package sourcecomment

const Contract = "nexa.dev/source-comment/v1"
