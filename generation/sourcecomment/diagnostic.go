package sourcecomment

import "sort"

type Code string

const (
	CodeInvalidSyntax        Code = "NEXA-SC001"
	CodeUnknownKey           Code = "NEXA-SC002"
	CodeInvalidValue         Code = "NEXA-SC003"
	CodeInvalidTarget        Code = "NEXA-SC004"
	CodeDuplicateFact        Code = "NEXA-SC005"
	CodeMisplacedFact        Code = "NEXA-SC006"
	CodeInheritedFactChanged Code = "NEXA-SC007"
	CodeInheritedNodeChanged Code = "NEXA-SC008"
	CodeSourceMismatch       Code = "NEXA-SC009"
	CodeSemanticCollision    Code = "NEXA-SC010"
)

type Diagnostic struct {
	Code           Code   `json:"code"`
	Category       string `json:"category"`
	File           string `json:"file"`
	Line           int    `json:"line"`
	Column         int    `json:"column"`
	Node           string `json:"node,omitempty"`
	FactID         string `json:"factID,omitempty"`
	EarliestSource string `json:"earliestSource,omitempty"`
	Expected       string `json:"expected,omitempty"`
	Actual         string `json:"actual,omitempty"`
	Suggestion     string `json:"suggestion"`
}

var categories = map[Code]string{
	CodeInvalidSyntax: "invalid_syntax", CodeUnknownKey: "unknown_key",
	CodeInvalidValue: "invalid_value", CodeInvalidTarget: "invalid_target",
	CodeDuplicateFact: "duplicate_fact", CodeMisplacedFact: "misplaced_fact",
	CodeInheritedFactChanged: "inherited_fact_changed", CodeInheritedNodeChanged: "inherited_node_changed",
	CodeSourceMismatch: "source_mismatch", CodeSemanticCollision: "semantic_collision",
}

func diagnostic(code Code, location Location, suggestion string) Diagnostic {
	return Diagnostic{Code: code, Category: categories[code], File: location.File, Line: location.Line, Column: location.Column, Suggestion: suggestion}
}

func sortDiagnostics(values []Diagnostic) {
	sort.SliceStable(values, func(i, j int) bool {
		left, right := values[i], values[j]
		if left.File != right.File {
			return left.File < right.File
		}
		if left.Line != right.Line {
			return left.Line < right.Line
		}
		if left.Column != right.Column {
			return left.Column < right.Column
		}
		if left.Code != right.Code {
			return left.Code < right.Code
		}
		return left.FactID < right.FactID
	})
}
