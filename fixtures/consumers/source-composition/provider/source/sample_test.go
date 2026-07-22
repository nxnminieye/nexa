package neutral

import "testing"

func TestMessage(t *testing.T) {
	if got := Message(); got != "materialized-neutral" {
		t.Fatalf("Message() = %q", got)
	}
}
