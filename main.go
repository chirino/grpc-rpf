package main

import (
	"github.com/chirino/grpc-rpf/internal/cmd/app"
	_ "github.com/chirino/grpc-rpf/internal/cmd/client"
	_ "github.com/chirino/grpc-rpf/internal/cmd/server"
	"math/rand"
	"time"
)

func main() {
	rand.Seed(time.Now().UTC().UnixNano())
	app.HandleErrorWithExitCode(app.Command.Execute, 1)
}
