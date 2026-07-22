package crudbuild

import (
	"strconv"
	"strings"
)

func Render(document Document) ([]byte, error) {
	if !document.Valid() {
		return nil, renderError("render_failed", "/document")
	}
	state := document.state
	var output strings.Builder
	output.WriteString("syntax = \"proto3\";\n\n")
	output.WriteString("package ")
	output.WriteString(state.protoPackage)
	output.WriteString(";\n\n")
	output.WriteString("option go_package = ")
	output.WriteString(strconv.Quote(state.goPackage))
	output.WriteString(";\n")
	if len(state.imports) > 0 {
		output.WriteByte('\n')
	}
	for _, item := range state.imports {
		output.WriteString("import ")
		output.WriteString(strconv.Quote(item))
		output.WriteString(";\n")
	}
	if len(state.enums)+len(state.messages)+len(state.services) > 0 {
		output.WriteByte('\n')
	}
	for _, enum := range state.enums {
		output.WriteString("enum ")
		output.WriteString(enum.name)
		output.WriteString(" {\n")
		if len(enum.reservedNumbers) > 0 {
			output.WriteString("  reserved ")
			for index, number := range enum.reservedNumbers {
				if index > 0 {
					output.WriteString(", ")
				}
				output.WriteString(strconv.FormatInt(int64(number), 10))
			}
			output.WriteString(";\n")
		}
		if len(enum.reservedNames) > 0 {
			output.WriteString("  reserved ")
			for index, name := range enum.reservedNames {
				if index > 0 {
					output.WriteString(", ")
				}
				output.WriteString(strconv.Quote(name))
			}
			output.WriteString(";\n")
		}
		for _, value := range enum.values {
			output.WriteString("  ")
			output.WriteString(value.name)
			output.WriteString(" = ")
			output.WriteString(strconv.FormatInt(int64(value.number), 10))
			output.WriteString(";\n")
		}
		output.WriteString("}\n\n")
	}
	for _, message := range state.messages {
		if !protoSymbolPattern.MatchString(message.name) {
			return nil, renderError("proto_symbol_invalid", "/messages")
		}
		output.WriteString("message ")
		output.WriteString(message.name)
		output.WriteString(" {\n")
		if len(message.reservedNumbers) > 0 {
			output.WriteString("  reserved ")
			for index, number := range message.reservedNumbers {
				if index > 0 {
					output.WriteString(", ")
				}
				output.WriteString(strconv.FormatInt(int64(number), 10))
			}
			output.WriteString(";\n")
		}
		if len(message.reservedNames) > 0 {
			output.WriteString("  reserved ")
			for index, name := range message.reservedNames {
				if index > 0 {
					output.WriteString(", ")
				}
				output.WriteString(strconv.Quote(name))
			}
			output.WriteString(";\n")
		}
		for _, field := range message.fields {
			output.WriteString("  ")
			if field.repeated {
				output.WriteString("repeated ")
			} else if field.optional {
				output.WriteString("optional ")
			}
			output.WriteString(field.wireType)
			output.WriteByte(' ')
			output.WriteString(field.name)
			output.WriteString(" = ")
			output.WriteString(strconv.FormatInt(int64(field.number), 10))
			output.WriteString(";\n")
		}
		output.WriteString("}\n\n")
	}
	for serviceIndex, service := range state.services {
		if !protoSymbolPattern.MatchString(service.name) {
			return nil, renderError("proto_symbol_invalid", "/services/"+itoa(serviceIndex)+"/name")
		}
		output.WriteString("service ")
		output.WriteString(service.name)
		output.WriteString(" {\n")
		for _, method := range service.methods {
			output.WriteString("  rpc ")
			output.WriteString(method.name)
			output.WriteString("(")
			output.WriteString(method.input)
			output.WriteString(") returns (")
			output.WriteString(method.output)
			if method.rpcContext != nil && len(method.rpcContext.contextFields) != 0 {
				output.WriteString(") {\n")
				output.WriteString("    option (nexa.protocol.v1.rpc_context) = {\n")
				for _, binding := range method.rpcContext.contextFields {
					output.WriteString("      context_fields: { source: TENANT_ID rpc_field: \"")
					output.WriteString(binding.rpcField)
					output.WriteString("\" }\n")
				}
				output.WriteString("    };\n  }\n")
			} else {
				output.WriteString(");\n")
			}
		}
		output.WriteString("}\n")
		if serviceIndex != len(state.services)-1 {
			output.WriteByte('\n')
		}
	}
	return []byte(output.String()), nil
}
