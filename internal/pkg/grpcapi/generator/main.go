//go:generate go run .

package main

import (
	"os"

	"github.com/chirino/hawtgo/sh"
)

func main() {

	sh := sh.New().
		Dir("..").
		CommandLog(os.Stdout).
		CommandLogPrefix(` running > `)

	sh.Line(`go install github.com/golang/protobuf/protoc-gen-go`).
		MustZeroExit()

	sh.Line(`protoc -I. --go_out=plugins=grpc,paths=source_relative:. service.proto`).
		MustZeroExit()

}
