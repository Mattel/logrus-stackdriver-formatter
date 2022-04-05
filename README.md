# logrus-stackdriver-formatter

[![Go Report Card](https://goreportcard.com/badge/github.com/Mattel/logrus-stackdriver-formatter)](https://goreportcard.com/report/github.com/Mattel/logrus-stackdriver-formatter)
[![GoDoc](https://img.shields.io/badge/godoc-reference-blue.svg?style=flat)](https://godoc.org/github.com/Mattel/logrus-stackdriver-formatter)
[![License MIT](https://img.shields.io/badge/license-MIT-lightgrey.svg?style=flat)](https://github.com/Mattel/logrus-stackdriver-formatter#license)

Logrus-stackdriver-formatter provides:
+ [logrus](https://github.com/sirupsen/logrus) formatter for Stackdriver.
+ [go-kit log](https://github.com/go-kit/kit/tree/master/log) adapter for the above.

In addition to supporting level-based logging to Stackdriver, for Error, Fatal and Panic levels it will append error context for [Error Reporting](https://cloud.google.com/error-reporting/).

## Installation

```shell
go get -u github.com/TV4/logrus-stackdriver-formatter
```

### Logrus Usage

```go
package main

import (

stackdriver "github.com/Mattel/logrus-stackdriver-formatter"
"github.com/sirupsen/logrus"
)

var log = logrus.New()

func init() {
    log.Formatter = stackdriver.NewFormatter(
        stackdriver.WithService("your-service"), 
        stackdriver.WithVersion("v0.1.0"),
    )
    log.Level = logrus.DebugLevel

    log.Info("ready to log!")
}
```

Here's a sample entry (prettified) from the example:

```json
{
  "serviceContext": {
    "service": "test-service",
    "version": "v0.1.0"
  },
  "message": "unable to parse integer: strconv.ParseInt: parsing \"text\": invalid syntax",
  "severity": "ERROR",
  "context": {
    "reportLocation": {
      "file": "github.com/Mattel/logrus-stackdriver-formatter/example_test.go",
      "line": 21,
      "function": "ExampleLogError"
    }
  }
}
```

### HTTP request context

If you'd like to add additional context like the `httpRequest`, here's a convenience function for creating a HTTP logger:

```go
func httpLogger(logger *logrus.Logger, r *http.Request) *logrus.Entry {
    return logger.WithFields(logrus.Fields{
        "httpRequest": map[string]interface{}{
            "method":    r.Method,
            "url":       r.URL.String(),
            "userAgent": r.Header.Get("User-Agent"),
            "referrer":  r.Header.Get("Referer"),
        },
    })
}
```

Then, in your HTTP handler, create a new context logger and all your log entries will have the HTTP request context appended to them:

```go
func handler(w http.ResponseWriter, r *http.Request) {
    httplog := httpLogger(log, r)
    // ...
    httplog.Infof("Logging with HTTP request context")
}
```

### Go-kit Log Adapter

Go-kit log is wrapped to encode conventions, enforce type-safety, provide leveled
logging, and so on. It can be used for both typical application log events,
and log-structured data streams.


### Typical application logging

```go
import (
	"os"
	logadapter "github.com/Mattel/logrus-stackdriver-formatter"
	kitlog "github.com/go-kit/kit/log"
)

func main() {
	w := kitlog.NewSyncWriter(os.Stderr)
	logger := logadapter.InitLogrusGoKitLogger(w)
	logger.Log("question", "what is the meaning of life?", "answer", 42)

	// Output:
	// question="what is the meaning of life?" answer=42
}
```

### Contextual Loggers

```go
import (
	"os"
	logadapter "github.com/Mattel/logrus-stackdriver-formatter"
	kitlog "github.com/go-kit/kit/log"
)

func main() {
	logger := logadapter.InitLogrusGoKitLogger(kitlog.NewSyncWriter(os.Stderr))
	logger = kitlog.With(logger, "instance_id", 123)

	logger.Log("msg", "starting")
	NewWorker(kitlog.With(logger, "component", "worker")).Run()
	NewSlacker(kitlog.With(logger, "component", "slacker")).Run()
}

// Output:
// instance_id=123 msg=starting
// instance_id=123 component=worker msg=running
// instance_id=123 component=slacker msg=running
```

## Enhancements

go-kit's `package log` is centered on the one-method Logger interface.

```go
type Logger interface {
	Log(keyvals ...interface{}) error
}
```

This interface, and its supporting code like is the product of much iteration
and evaluation. For more details on the evolution of the Logger interface,
see [The Hunt for a Logger Interface](http://go-talks.appspot.com/github.com/ChrisHines/talks/structured-logging/structured-logging.slide#1),
a talk by [Chris Hines](https://github.com/ChrisHines).
Also, please see
[#63](https://github.com/go-kit/kit/issues/63),
[#76](https://github.com/go-kit/kit/pull/76),
[#131](https://github.com/go-kit/kit/issues/131),
[#157](https://github.com/go-kit/kit/pull/157),
[#164](https://github.com/go-kit/kit/issues/164), and
[#252](https://github.com/go-kit/kit/pull/252)
to review historical conversations about package log and the Logger interface.

Value-add packages and suggestions,
like improvements to [the leveled logger](https://godoc.org/github.com/go-kit/kit/log/level),
are of course welcome. Good proposals should

- Be composable with [contextual loggers](https://godoc.org/github.com/go-kit/kit/log#With),
- Not break the behavior of [log.Caller](https://godoc.org/github.com/go-kit/kit/log#Caller) in any wrapped contextual loggers, and
- Be friendly to packages that accept only an unadorned log.Logger.
ber-common/zap](https://github.com/uber-common/zap), a zero-alloc logging library, includes a comparison with kit/log

## Alternatives

+ Both [go-kit log](https://github.com/go-kit/kit/tree/master/log) and [logr](https://github.com/go-logr/logr) both provide to developers clean, minimal surface area for sending log messages. However, neither has direct, first-class stackdriver support.
+ Uber's [Zap](https://github.com/uber-go/zap) is very performant, but it's SugaredLogger API is unfamiliar to me.

So one choice could be [logr](https://github.com/go-logr/logr) -> [zapr](https://github.com/go-logr/zapr) -> [zap](https://github.com/uber-go/zap) -> [zap-stackdriver](https://github.com/tommy351/zap-stackdriver) or [zapdriver](github.com/blendle/zapdriver) but zapr still uses dep, didn't have go module support.

I could also have gone with [go-kit log](https://github.com/go-kit/kit/tree/master/log) -> [zap](https://github.com/uber-go/zap) -> [zap-stackdriver](https://github.com/tommy351/zap-stackdriver) or [zapdriver](github.com/blendle/zapdriver).

Instead, I pulled a bunch of people's forks together into this piece of mad science because it seemed like it would work. ¯\\\_(ツ)_/¯