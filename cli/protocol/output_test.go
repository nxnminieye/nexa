package protocol_test

import (
	"testing"

	"github.com/nxnminieye/nexa/cli/protocol"
)

func TestCompactJSONRequested(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want bool
	}{
		{name: "absent", args: []string{"inspect"}, want: false},
		{name: "flag", args: []string{"inspect", "--json"}, want: true},
		{name: "explicit true", args: []string{"--json=true", "inspect"}, want: true},
		{name: "explicit false", args: []string{"inspect", "--json=false"}, want: false},
		{name: "last valid value wins", args: []string{"--json", "--json=false", "inspect"}, want: false},
		{name: "terminator stops parsing", args: []string{"inspect", "--", "--json"}, want: false},
		{name: "value before terminator remains", args: []string{"--json", "--", "--json=false"}, want: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := protocol.CompactJSONRequested(tt.args); got != tt.want {
				t.Fatalf("CompactJSONRequested(%q) = %v, want %v", tt.args, got, tt.want)
			}
		})
	}
}
