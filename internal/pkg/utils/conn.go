package utils

import (
	"bytes"
	"context"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"io"
	"log"
	"net"
	"sync"
	"sync/atomic"
)

type Conn interface {
	Read(b []byte) (n int, err error)
	Write(b []byte) (n int, err error)
	Close() error
}

type WrapperConn struct {
	Send    func([]byte) error
	Recv    func() ([]byte, error)
	Context context.Context
	Cancel  context.CancelFunc
	reader  bytes.Reader
}

var _ Conn = &WrapperConn{}

func (w *WrapperConn) Read(data []byte) (n int, err error) {
	if w.reader.Len() == 0 {
		select {
		case <-w.Context.Done():
			return 0, io.EOF
		default:
		}
		x, err := w.Recv()
		if err != nil {
			return n, err
		}
		w.reader.Reset(x)
	}
	return w.reader.Read(data)
}

func (w *WrapperConn) Write(data []byte) (n int, err error) {
	err = w.Send(data)
	if err != nil {
		return 0, err
	}
	return len(data), err
}

func (w *WrapperConn) Close() error {
	w.Cancel()
	return nil
}

func Pump(wg *sync.WaitGroup, conn1 Conn, conn2 Conn, isClosed *int32, log *log.Logger) {
	wg.Add(1)
	go func() {
		defer wg.Done()
		_, err := io.Copy(conn1, conn2)
		if err != nil {

			// Unwrap the error if it's a net.OpError ...
			switch t := err.(type) {
			case *net.OpError:
				err = t.Err
			}
			if err == io.EOF {
				return
			}
			if atomic.LoadInt32(isClosed) == 1 {
				return
			}
			if status.Code(err) == codes.Canceled {
				return
			}

			log.Printf("error forwarding data: %v", err)
			return
		}
	}()
}
