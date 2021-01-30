package utils

import "fmt"

type PrefixingWriter string

func (p PrefixingWriter) Write(data []byte) (n int, err error) {
	fmt.Println(p, string(data))
	return len(data), nil
}
