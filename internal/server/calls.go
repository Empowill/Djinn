package server

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/empowill/djinn/internal/logx"
)

// errorMax is how much of a call's error the log keeps.
const errorMax = 200

// logged logs each call h answers (W511): its method, how long it took, and its error's code and message. A unary call
// logs at info, at warn past logx.Slow; a stream, which lasts as long as the window watches, logs at debug.
func logged(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		cw := &callWriter{ResponseWriter: w}
		ct := r.Header.Get("Content-Type")
		stream := strings.HasPrefix(ct, "application/connect+") || strings.HasPrefix(ct, "application/grpc")
		defer func() {
			args := []any{"method", r.URL.Path}
			if cw.status != 0 && cw.status != http.StatusOK {
				args = append(args, "status", cw.status)
				var e struct{ Code, Message string }
				if json.Unmarshal(cw.body, &e) == nil && e.Code != "" {
					if len(e.Message) > errorMax {
						e.Message = e.Message[:errorMax] + "…"
					}
					args = append(args, "code", e.Code, "error", e.Message)
				}
			}
			if stream {
				args = append(args, "took", time.Since(start).Round(time.Millisecond))
				logx.L().Debug("stream", args...)
				return
			}
			logx.Took(slog.LevelInfo, "call", start, args...)
		}()
		h.ServeHTTP(cw, r)
	})
}

// callWriter keeps the status of a response, and the start of its body when the status is an error.
type callWriter struct {
	http.ResponseWriter
	status int
	body   []byte
}

func (w *callWriter) WriteHeader(code int) {
	if w.status == 0 {
		w.status = code
	}
	w.ResponseWriter.WriteHeader(code)
}

func (w *callWriter) Write(b []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	if w.status != http.StatusOK && len(w.body) < 1024 {
		w.body = append(w.body, b[:min(len(b), 1024-len(w.body))]...)
	}
	return w.ResponseWriter.Write(b)
}

// Flush keeps the streams working, as answerWriter's.
func (w *callWriter) Flush() {
	_ = http.NewResponseController(w.ResponseWriter).Flush()
}

// Unwrap lets an http.ResponseController reach the writer underneath.
func (w *callWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }
