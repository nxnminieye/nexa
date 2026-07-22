package protocol

import "strconv"

// CompactJSONRequested reports the envelope output mode requested before the
// argument terminator. The last valid --json value wins.
func CompactJSONRequested(args []string) bool {
	compact := false
	for _, argument := range args {
		if argument == "--" {
			break
		}
		switch {
		case argument == "--json":
			compact = true
		case len(argument) > len("--json=") && argument[:len("--json=")] == "--json=":
			if value, err := strconv.ParseBool(argument[len("--json="):]); err == nil {
				compact = value
			}
		}
	}
	return compact
}
