package composition

import (
	"sort"
	"strconv"
	"strings"
)

func renderRegister(operations []*operationState) ([]byte, error) {
	ids := make([]string, len(operations))
	for index, operation := range operations {
		ids[index] = operation.proxy.OperationID()
	}
	sort.Strings(ids)
	var source strings.Builder
	source.WriteString("package generated\n\ntype Operation struct { ID string }\nfunc Operations() []Operation { return []Operation{\n")
	for _, id := range ids {
		source.WriteString("{ID: " + strconv.Quote(id) + "},\n")
	}
	source.WriteString("}\n}\n")
	return formatted(source.String())
}
