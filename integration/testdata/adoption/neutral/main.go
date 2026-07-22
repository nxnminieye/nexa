package main

import (
	"github.com/nxnminieye/nexa/nexactl/host"
)

func main() {
	_, err := host.New(host.Options{Name: "nexactl", Version: "local"})
	if err != nil {
		panic(err)
	}
}
