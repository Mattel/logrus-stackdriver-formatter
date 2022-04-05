// Package logadapter provides an adapter to the
// go-kit log.Logger interface.
package logadapter

import (
	"fmt"

	gokitlog "github.com/go-kit/kit/log"
	"github.com/sirupsen/logrus"
)

// LogrusGoKitLogger is a gokit-compatible wrapper for logrus.LogrusGoKitLogger
type LogrusGoKitLogger struct {
	logrusLogger
}

type logrusLogger interface {
	WithFields(fields logrus.Fields) *logrus.Entry
}

// NewLogrusGoKitLogger creates a gokit-compatible logger
func NewLogrusGoKitLogger(logger logrusLogger) *LogrusGoKitLogger {
	return &LogrusGoKitLogger{logger}
}

const (
	msgKey      = "msg"
	messageKey  = "message"
	errKey      = "err"
	errorKey    = "error"
	severityKey = "severity"
	levelKey    = "level"
)

// Log implements the fundamental Logger interface
func (l LogrusGoKitLogger) Log(keyvals ...interface{}) error {
	fields, level, msg := l.extractLogElements(keyvals...)

	entry := l.WithFields(fields)
	entry.Log(level, msg)

	return nil
}

// extractLogElements iterates through the keyvals to form well
// structured key:value pairs that Logrus expects. It also checks for keys with
// special meaning like "msg" and "level" to format the log entry
func (l LogrusGoKitLogger) extractLogElements(
	keyVals ...interface{},
) (logrus.Fields, logrus.Level, string) {
	msg := ""
	fields := logrus.Fields{}
	level := logrus.DebugLevel

	for i := 0; i < len(keyVals); i += 2 {
		fieldKey := fmt.Sprint(keyVals[i])
		if i+1 < len(keyVals) {
			fieldValue := fmt.Sprint(keyVals[i+1])
			switch {
			case (fieldKey == msgKey || fieldKey == messageKey) && msg == "":
				// if this is a "msg" key, store it separately so we can use it as the
				// main log message
				msg = fieldValue
			case fieldKey == errKey || fieldKey == errorKey:
				// if this is a "err" key, we should use the error message as
				// the main message and promote the level to Error
				err := fieldValue
				if err != "" {
					msg = err
					level = logrus.ErrorLevel
				}
			case fieldKey == levelKey || fieldKey == severityKey:
				// if this is a "level" key, it means GoKit logger is giving us
				// a hint to the logging level
				levelStr := fieldValue
				parsedLevel, err := logrus.ParseLevel(levelStr)
				if err != nil || level < parsedLevel {
					level = logrus.ErrorLevel
					fields[levelKey] = levelStr
				} else {
					level = parsedLevel
				}
			default:
				// this is just regular log data, add it as a key:value pair
				fields[fieldKey] = keyVals[i+1]
			}
		} else {
			// odd pair key, with no matching value
			fields[fieldKey] = gokitlog.ErrMissingValue
		}
	}
	return fields, level, msg
}
