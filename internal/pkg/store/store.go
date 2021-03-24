package store

import (
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type Store interface {
	Start() error
	Stop()
	OnListen(service string, token string, from string) (redirect string, close func(), err error)
	OnConnect(service string, token string, from string) (redirect string, close func(), err error)
}

var PermissionDenied = status.Error(codes.PermissionDenied, "permission denied")
var ServiceNotFound = status.Error(codes.NotFound, "service not found")
