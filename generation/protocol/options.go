package protocol

import _ "embed"

const optionsProtoPath = "nexa/protocol/v1/options.proto"

//go:embed nexa/protocol/v1/options.proto
var embeddedOptionsProto []byte

func OptionsProto() []byte { return append([]byte(nil), embeddedOptionsProto...) }
