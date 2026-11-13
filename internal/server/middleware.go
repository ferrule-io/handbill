package server

import (
	"log/slog"
	"net/http"
	"strings"
	"time"
)

// recoverPanics turns a handler panic into a 500 and logs it.
// http.ErrAbortHandler is re-panicked so net/http aborts the response.
func recoverPanics(log *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			v := recover()
			if v == nil {
				return
			}
			if v == http.ErrAbortHandler {
				panic(v)
			}
			log.Error("handler panic", "panic", v, "method", r.Method, "path", LogPath(r.URL.Path))
			http.Error(w, "internal server error", http.StatusInternalServerError)
		}()
		next.ServeHTTP(w, r)
	})
}

// logRequests writes one structured line per request. Query strings are
// never logged (they carry tickets and OAuth codes); see LogPath.
func logRequests(log *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		sw := &statusWriter{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(sw, r)
		if r.URL.Path == "/healthz" || r.URL.Path == "/readyz" {
			return
		}
		log.Info("request",
			"method", r.Method,
			"host", r.Host,
			"path", LogPath(r.URL.Path),
			"status", sw.status,
			"bytes", sw.bytes,
			"duration_ms", time.Since(start).Milliseconds(),
		)
	})
}

// LogPath returns a request path safe to log: paths under /_auth/ carry
// tokens (e.g. /_auth/share/<token>), so only their first segment is kept.
func LogPath(p string) string {
	rest, ok := strings.CutPrefix(p, "/_auth/")
	if !ok {
		return p
	}
	seg, _, more := strings.Cut(rest, "/")
	if more {
		return "/_auth/" + seg + "/…"
	}
	return p
}

type statusWriter struct {
	http.ResponseWriter
	status      int
	bytes       int64
	wroteHeader bool
}

func (w *statusWriter) WriteHeader(code int) {
	if !w.wroteHeader {
		w.status, w.wroteHeader = code, true
	}
	w.ResponseWriter.WriteHeader(code)
}

func (w *statusWriter) Write(b []byte) (int, error) {
	w.wroteHeader = true
	n, err := w.ResponseWriter.Write(b)
	w.bytes += int64(n)
	return n, err
}

// Flush keeps http.Flusher assertions working for streaming responses.
func (w *statusWriter) Flush() {
	w.wroteHeader = true
	_ = http.NewResponseController(w.ResponseWriter).Flush()
}

// Unwrap lets http.ResponseController reach the underlying writer (flush,
// deadlines), which streaming handlers such as /mcp rely on.
func (w *statusWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }
