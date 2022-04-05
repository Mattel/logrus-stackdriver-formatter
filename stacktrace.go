package logadapter

import (
	"bytes"
	"errors"
	"fmt"
	"runtime"
	"strings"

	pkgErrors "github.com/pkg/errors"
)

type stackTracer interface {
	StackTrace() pkgErrors.StackTrace
}

// extractStack pulls a call stack extracted from an error with GCP Error Reporting
// see: https://github.com/googleapis/google-cloud-go/issues/1084
func extractStackFromError(err error) []byte {
	if err == nil {
		return nil
	}

	var st stackTracer
	if !errors.As(err, &st) {
		return nil
	}

	buf := bytes.Buffer{}
	// hardcode, I haven't access to this on the call stack
	buf.WriteString(fmt.Sprintf("%s\ngoroutine 1 [running]:\n", err.Error()))

	var lines []string
	for _, frame := range st.StackTrace() {
		pc := uintptr(frame) - 1
		fn := runtime.FuncForPC(pc)
		if fn != nil {
			file, line := fn.FileLine(pc)
			lines = append(
				lines,
				fmt.Sprintf("%s()\n\t%s:%d +%#x", fn.Name(), file, line, fn.Entry()),
			)
		}
	}
	buf.WriteString(strings.Join(lines, "\n"))

	return buf.Bytes()
}
