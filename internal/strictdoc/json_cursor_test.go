package strictdoc

import "testing"

func TestJSONPositionCursorPreservesMonotonicByteCoordinates(t *testing.T) {
	data := []byte("aé\n中x")
	cursor := newJSONPositionCursor(data)
	tests := []struct {
		offset int64
		line   int
		column int
	}{
		{0, 1, 1},
		{1, 1, 2},
		{3, 1, 4},
		{4, 2, 1},
		{7, 2, 4},
		{8, 2, 5},
	}
	for _, test := range tests {
		line, column := cursor.lineColumn(test.offset)
		if line != test.line || column != test.column {
			t.Fatalf("lineColumn(%d) = %d:%d, want %d:%d", test.offset, line, column, test.line, test.column)
		}
	}
	if cursor.offset != len(data) {
		t.Fatalf("cursor offset = %d, want %d", cursor.offset, len(data))
	}

	line, column := cursor.lineColumn(1)
	if line != 1 || column != 2 || cursor.offset != len(data) {
		t.Fatalf("backward lookup = %d:%d at offset %d, want 1:2 without rewinding", line, column, cursor.offset)
	}
}
