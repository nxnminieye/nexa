package crudproto

import _ "embed"

//go:embed crud-protocol-ir-v2.schema.json
var embeddedIRSchema []byte

//go:embed crud-protocol-lock-v1.schema.json
var embeddedLockSchema []byte

func IRSchema() []byte   { return append([]byte(nil), embeddedIRSchema...) }
func LockSchema() []byte { return append([]byte(nil), embeddedLockSchema...) }
