package server

import (
	"encoding/binary"
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/empowill/djinn/internal/logx"
)

const (
	// errorMax is how much of a call's error the log keeps.
	errorMax = 200
	// internalMax is how much of an internal error the log keeps: its cause comes last.
	internalMax = 2000
	// bodyMax is how much of an error's body a call keeps to log it.
	bodyMax = 4 << 10
)

// logged logs each call h answers (W511): its method, how long it took, and its error's code and message. A unary call
// logs at info, at warn past logx.Slow; a stream, which lasts as long as the window watches, logs at debug. A call that
// fails with an internal error (internal, unknown, data loss) logs at error, unary or stream, with its whole message. A
// panic of h logs at error with its stack and answers 500 when nothing was written yet (W525): djinn up goes on.
func logged(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		ct := r.Header.Get("Content-Type")
		stream := strings.HasPrefix(ct, "application/connect+") || strings.HasPrefix(ct, "application/grpc")
		cw := &callWriter{ResponseWriter: w, stream: strings.HasPrefix(ct, "application/connect+")}
		defer func() {
			if p := recover(); p != nil {
				answerPanic(cw, r, p)
				return
			}
			level := slog.LevelInfo
			args := []any{"method", r.URL.Path}
			var e struct{ Code, Message string }
			if cw.status != 0 && cw.status != http.StatusOK {
				args = append(args, "status", cw.status)
				json.Unmarshal(cw.body, &e) //nolint:errcheck // Not a Connect error: the status tells enough.
			} else if cw.end != nil {
				var end struct {
					Error *struct{ Code, Message string }
				}
				if json.Unmarshal(cw.end, &end) == nil && end.Error != nil {
					e = *end.Error
				}
			}
			if e.Code != "" {
				limit := errorMax
				if internal(e.Code) {
					level, limit = slog.LevelError, internalMax
				}
				if len(e.Message) > limit {
					e.Message = e.Message[:limit] + "…"
				}
				args = append(args, "code", e.Code, "error", e.Message)
			}
			if stream {
				if level < slog.LevelError {
					level = slog.LevelDebug
				}
				args = append(args, "took", time.Since(start).Round(time.Millisecond))
				logx.L().Log(r.Context(), level, "stream", args...)
				return
			}
			logx.Took(level, "call", start, args...)
		}()
		h.ServeHTTP(cw, r)
	})
}

// internal tells whether a Connect error's code says Djinn failed, not the caller.
func internal(code string) bool {
	return code == "internal" || code == "unknown" || code == "data_loss"
}

// recovered answers a panic of h as logged does, for what is not a service: the interface's files.
func recovered(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		aw := &answerWriter{ResponseWriter: w}
		defer func() {
			if p := recover(); p != nil {
				answerPanic(aw, r, p)
			}
		}()
		h.ServeHTTP(aw, r)
	})
}

// answerPanic logs p, the panic of the call r, at error with its stack, and answers 500, an internal Connect error,
// when w has not answered yet; a stream that has started only ends. http.ErrAbortHandler, the way net/http aborts a
// response on purpose, panics on.
func answerPanic(w interface {
	http.ResponseWriter
	written() bool
}, r *http.Request, p any) {
	if p == http.ErrAbortHandler { //nolint:errorlint // net/http panics with this very value.
		panic(p)
	}
	logx.Panicked("call", p, "method", r.URL.Path)
	if w.written() {
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusInternalServerError)
	w.Write([]byte(`{"code":"internal","message":"djinn: internal error, see logs/djinn.log"}`)) //nolint:errcheck // The caller is gone.
}

// callWriter keeps the status of a response, and the start of its body when the status is an error. Of a Connect
// stream, it keeps the end of the stream, which carries its error.
type callWriter struct {
	http.ResponseWriter
	status int
	body   []byte
	stream bool
	prefix [5]byte // the last envelope prefix written alone, as connect-go writes it before its data
	ends   bool    // prefix is the one of the end of the stream
	end    []byte  // the end of the stream: {"error":{…}} when it failed
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
	if w.status != http.StatusOK && len(w.body) < bodyMax {
		w.body = append(w.body, b[:min(len(b), bodyMax-len(w.body))]...)
	}
	if w.stream {
		w.envelope(b)
	}
	return w.ResponseWriter.Write(b)
}

// envelope keeps the end of a Connect stream: connect-go writes an envelope's 5 bytes of prefix (flags, then the
// length), then its data. The end's flag is 2.
func (w *callWriter) envelope(b []byte) {
	ends := w.ends
	w.ends = false
	switch {
	case len(b) == len(w.prefix):
		copy(w.prefix[:], b)
		w.ends = b[0]&2 != 0
	case ends && uint32(len(b)) == binary.BigEndian.Uint32(w.prefix[1:]) && len(b) <= bodyMax:
		w.end = append(w.end[:0], b...)
	}
}

func (w *callWriter) written() bool { return w.status != 0 }

// Flush keeps the streams working, as answerWriter's.
func (w *callWriter) Flush() {
	_ = http.NewResponseController(w.ResponseWriter).Flush()
}

// Unwrap lets an http.ResponseController reach the writer underneath.
func (w *callWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }
