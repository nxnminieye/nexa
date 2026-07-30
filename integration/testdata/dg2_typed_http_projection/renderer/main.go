package main

import (
	"bytes"
	"fmt"
	"go/format"
	"io"
	"os"
	"path/filepath"
	"strconv"

	apiFormat "github.com/zeromicro/go-zero/tools/goctl/pkg/parser/api/format"
	goctlparser "github.com/zeromicro/go-zero/tools/goctl/pkg/parser/api/parser"
)

const version = "dg2-api-source-renderer v1.0.0"

func main() {
	if len(os.Args) == 2 && os.Args[1] == "version" {
		fmt.Println(version)
		return
	}
	if len(os.Args) != 9 || os.Args[1] != "api" || os.Args[2] != "generate" || os.Args[3] != "--service" || os.Args[4] != "core" || os.Args[5] != "--entry-file" || os.Args[7] != "--generated-scope" {
		fatal("invalid arguments")
	}
	entry, scope := os.Args[6], os.Args[8]
	if filepath.IsAbs(entry) || filepath.Clean(entry) != entry || !filepath.IsLocal(entry) || filepath.Ext(entry) != ".api" {
		fatal("invalid API source entry")
	}
	if filepath.IsAbs(scope) || filepath.Clean(scope) != scope || !filepath.IsLocal(scope) {
		fatal("invalid generated scope")
	}
	stdin, err := io.ReadAll(os.Stdin)
	if err != nil || len(stdin) != 0 {
		fatal("serialized HTTP facts are forbidden")
	}
	source, err := os.ReadFile(entry)
	if err != nil {
		fatal("read API source: " + err.Error())
	}
	parsed, err := goctlparser.Parse(entry, source)
	if err != nil || parsed.Validate() != nil {
		fatal("invalid API source")
	}
	var formattedAPI bytes.Buffer
	if err := apiFormat.Source(source, &formattedAPI); err != nil {
		fatal("format API source: " + err.Error())
	}
	client, err := format.Source([]byte("package generated\n\nconst APISourceEntry = " + strconv.Quote(filepath.ToSlash(entry)) + "\n"))
	if err != nil {
		fatal(err.Error())
	}
	for name, content := range map[string][]byte{"account.generated.api": formattedAPI.Bytes(), "client.generated.go": client} {
		target := filepath.Join(scope, name)
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			fatal(err.Error())
		}
		if err := os.WriteFile(target, content, 0o644); err != nil {
			fatal(err.Error())
		}
	}
}

func fatal(message string) {
	fmt.Fprintln(os.Stderr, message)
	os.Exit(1)
}
