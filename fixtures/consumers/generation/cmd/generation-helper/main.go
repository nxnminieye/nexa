package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/gowebpki/jcs"
	genprotocol "github.com/nxnminieye/nexa/generation/protocol"
)

const helperVersion = "consumer-generation-helper v1.0.0"

var outputs = map[string]map[string]string{
	"rpc": {
		"account.generated.proto": "syntax = \"proto3\";\npackage generated.account.v1;\nmessage Account { string name = 1; }\n",
		"account.generated.go":    "package generated\n\ntype Account struct{ Name string }\n",
	},
	"api": {
		"core.generated.api": "syntax = \"v1\"\ninfo (nexaContractVersion: \"nexa.dev/http-api/v1\")\ntype GeneratedHealthRequest {}\ntype GeneratedHealthResponse { OK bool }\n@server (nexaOperationId: \"generated.health\" nexaAuthMode: \"none\")\nservice generated-api { @handler generatedHealth get /generated/health (GeneratedHealthRequest) returns (GeneratedHealthResponse) }\n",
		"core.generated.go":  "package generated\n\nconst HealthPath = \"/generated/health\"\n",
	},
}

func main() {
	if len(os.Args) == 2 && os.Args[1] == "version" {
		fmt.Println(helperVersion)
		return
	}
	if len(os.Args) < 7 || os.Args[2] != "generate" || os.Args[3] != "--service" {
		fatal("invalid arguments")
	}
	family, service := os.Args[1], os.Args[4]
	argument := 5
	entryFile := ""
	if argument+1 < len(os.Args) && os.Args[argument] == "--entry-file" {
		entryFile = os.Args[argument+1]
		argument += 2
	}
	if argument+1 >= len(os.Args) || os.Args[argument] != "--generated-scope" {
		fatal("invalid arguments")
	}
	scope := os.Args[argument+1]
	if scope == "" || filepath.IsAbs(scope) || filepath.Clean(scope) != scope || !filepath.IsLocal(scope) {
		fatal("invalid generated scope")
	}
	if family == "api" {
		if entryFile == "" || filepath.IsAbs(entryFile) || filepath.Ext(entryFile) != ".api" {
			fatal("API source entry is invalid")
		}
		if _, err := os.ReadFile(entryFile); err != nil {
			fatal("API source entry is unavailable")
		}
	}
	switch family {
	case "rpc":
		input, err := io.ReadAll(os.Stdin)
		if err != nil {
			fatal(err.Error())
		}
		canonical, err := jcs.Transform(input)
		if err != nil || !bytes.Equal(canonical, input) {
			fatal("facts are not canonical JSON")
		}
		var identity struct {
			APIVersion string `json:"apiVersion"`
			Kind       string `json:"kind"`
			ServiceID  string `json:"serviceId"`
		}
		if err := json.Unmarshal(input, &identity); err != nil {
			fatal("facts are invalid JSON")
		}
		if identity.APIVersion != genprotocol.APIVersion || identity.Kind != genprotocol.Kind || identity.ServiceID != service {
			fatal("RPC facts do not match the selected service")
		}
	case "api":
		// API generation reads the explicitly declared .api source above.
	default:
		fatal("unknown generation family")
	}
	for name, content := range outputs[family] {
		name = filepath.Join(scope, name)
		if err := os.MkdirAll(filepath.Dir(name), 0o755); err != nil {
			fatal(err.Error())
		}
		if err := os.WriteFile(name, []byte(content), 0o644); err != nil {
			fatal(err.Error())
		}
	}
}

func fatal(message string) {
	fmt.Fprintln(os.Stderr, message)
	os.Exit(1)
}
