package sourcecomment

import (
	"fmt"
	"path"
	"regexp"
	"strings"
)

var sourceSymbolPattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_.:/-]*$`)

type SourceRef struct {
	stage  Stage
	path   string
	symbol string
}

func ParseSourceRef(raw string) (SourceRef, error) {
	separator := strings.Index(raw, "://")
	fragment := strings.LastIndex(raw, "#")
	if separator <= 0 || fragment <= separator+3 || fragment == len(raw)-1 || strings.Contains(raw[fragment+1:], "#") {
		return SourceRef{}, fmt.Errorf("source reference must use <stage>://<path>#<symbol>")
	}
	stage := Stage(raw[:separator])
	if stage != StageEnt && stage != StageProto && stage != StageAPI && stage != StagePage {
		return SourceRef{}, fmt.Errorf("source reference stage is invalid")
	}
	filePath, symbol := raw[separator+3:fragment], raw[fragment+1:]
	if filePath == "" || strings.HasPrefix(filePath, "/") || strings.Contains(filePath, "\\") || strings.ContainsAny(filePath, "?#") || path.Clean(filePath) != filePath {
		return SourceRef{}, fmt.Errorf("source reference path is not canonical")
	}
	for _, segment := range strings.Split(filePath, "/") {
		if segment == "" || segment == "." || segment == ".." {
			return SourceRef{}, fmt.Errorf("source reference path is not canonical")
		}
	}
	if !sourceSymbolPattern.MatchString(symbol) {
		return SourceRef{}, fmt.Errorf("source reference symbol is invalid")
	}
	return SourceRef{stage: stage, path: filePath, symbol: symbol}, nil
}

func (r SourceRef) Stage() Stage   { return r.stage }
func (r SourceRef) Path() string   { return r.path }
func (r SourceRef) Symbol() string { return r.symbol }
func (r SourceRef) Valid() bool    { return r.stage.order() >= 0 && r.path != "" && r.symbol != "" }
func (r SourceRef) String() string {
	if !r.Valid() {
		return ""
	}
	return string(r.stage) + "://" + r.path + "#" + r.symbol
}
