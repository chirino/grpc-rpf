package main

import (
	"github.com/chirino/hsvcproxy/internal/cmd/app"
	"math/rand"
	"time"
)

func main() {
	rand.Seed(time.Now().UTC().UnixNano())
	app.HandleErrorWithExitCode(app.Command.Execute, 1)
}
