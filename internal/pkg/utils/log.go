package utils

import "log"

func AddLogPrefix(l *log.Logger, prefix string) *log.Logger {
	return log.New(l.Writer(), l.Prefix()+prefix, l.Flags())
}
