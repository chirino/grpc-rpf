package main

import (
	"github.com/chirino/grpc-rpf/internal/command/app"
	_ "github.com/chirino/grpc-rpf/internal/command/exporter"
	_ "github.com/chirino/grpc-rpf/internal/command/importer"
	_ "github.com/chirino/grpc-rpf/internal/command/server"
	"math/rand"
	"time"
)

func main() {

	rand.Seed(time.Now().UTC().UnixNano())
	app.HandleErrorWithExitCode(app.Command.Execute, 1)
}
