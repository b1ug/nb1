package exchange

import (
	"bitbucket.org/neiku/hlog"
	"go.uber.org/zap"
)

var log *zap.SugaredLogger

func init() {
	log = hlog.NewNoopLogger().SugaredLogger
}

// SetLog sets the logger from outside the package.
func SetLog(l *zap.SugaredLogger) {
	log = l
}
