package main

import (
	"github.com/chirino/grpc-rpf/internal/command/app"
	_ "github.com/chirino/grpc-rpf/internal/command/exporter"
	_ "github.com/chirino/grpc-rpf/internal/command/importer"
	_ "github.com/chirino/grpc-rpf/internal/command/server"
	v "github.com/chirino/grpc-rpf/internal/command/version"
	"math/rand"
	"time"
)

var version = ""
var commit = ""

func main() {
	if version != "" && version != "v0.0.0-next" {
		v.Version = version
	} else if commit != "" {
		v.Version = "commit#" + commit
	}

	rand.Seed(time.Now().UTC().UnixNano())
	app.HandleErrorWithExitCode(app.Command.Execute, 1)
}
