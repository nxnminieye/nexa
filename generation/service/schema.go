package service

import _ "embed"

//go:embed service-manifest-v1.schema.json
var embeddedSchema []byte

func Schema() []byte { return append([]byte(nil), embeddedSchema...) }
