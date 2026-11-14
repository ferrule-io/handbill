package httpserver

import (
	"context"
	"io"
	"log/slog"
	"net"
	"net/http"
	"testing"
	"time"
)

var discard = slog.New(slog.DiscardHandler)

func listen(t *testing.T) net.Listener {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	return ln
}

func TestNewSetsLimits(t *testing.T) {
	s := New(":0", http.NotFoundHandler(), discard)
	if s.ReadHeaderTimeout != ReadHeaderTimeout || s.ReadTimeout != ReadTimeout || s.WriteTimeout != WriteTimeout ||
		s.IdleTimeout != IdleTimeout || s.MaxHeaderBytes != MaxHeaderBytes || s.ErrorLog == nil {
		t.Fatalf("server limits: %+v", s)
	}
}

func TestGracefulShutdownFinishesInFlightRequests(t *testing.T) {
	started, release := make(chan struct{}), make(chan struct{})
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(started)
		<-release
		_, _ = io.WriteString(w, "done")
	})
	ln := listen(t)
	url := "http://" + ln.Addr().String()
	ctx, cancel := context.WithCancel(context.Background())
	served := make(chan error, 1)
	go func() { served <- Serve(ctx, New("", h, discard), ln, 5*time.Second, discard) }()

	got := make(chan string, 1)
	go func() {
		res, err := http.Get(url)
		if err != nil {
			got <- "error: " + err.Error()
			return
		}
		defer res.Body.Close()
		b, _ := io.ReadAll(res.Body)
		got <- string(b)
	}()
	<-started
	cancel() // SIGTERM

	// The listener closes at once: new connections are refused...
	deadline := time.Now().Add(2 * time.Second)
	for {
		c, err := net.DialTimeout("tcp", ln.Addr().String(), 100*time.Millisecond)
		if err != nil {
			break
		}
		c.Close()
		if time.Now().After(deadline) {
			t.Fatal("listener still accepting after shutdown began")
		}
		time.Sleep(10 * time.Millisecond)
	}
	// ...while the in-flight request is still allowed to finish.
	select {
	case err := <-served:
		t.Fatalf("Serve returned before the in-flight request finished: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	close(release)
	if body := <-got; body != "done" {
		t.Fatalf("in-flight request: %q", body)
	}
	if err := <-served; err != nil {
		t.Fatalf("Serve: %v", err)
	}
}

func TestShutdownCutsOffAfterDrainTimeout(t *testing.T) {
	started := make(chan struct{})
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(started)
		<-r.Context().Done() // never finishes on its own
	})
	ln := listen(t)
	ctx, cancel := context.WithCancel(context.Background())
	served := make(chan error, 1)
	go func() { served <- Serve(ctx, New("", h, discard), ln, 200*time.Millisecond, discard) }()
	go func() {
		if res, err := http.Get("http://" + ln.Addr().String()); err == nil {
			res.Body.Close()
		}
	}()
	<-started
	begin := time.Now()
	cancel()
	select {
	case err := <-served:
		if err != nil {
			t.Fatalf("Serve: %v", err)
		}
		if d := time.Since(begin); d < 200*time.Millisecond {
			t.Fatalf("Serve returned after %v, before the drain timeout", d)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Serve did not return after the drain timeout")
	}
}

// slowBody sends n bytes with a pause between each.
type slowBody struct {
	n     int
	pause time.Duration
}

func (s *slowBody) Read(p []byte) (int, error) {
	if s.n == 0 {
		return 0, io.EOF
	}
	time.Sleep(s.pause)
	s.n--
	p[0] = 'x'
	return 1, nil
}

func TestExtendLiftsTheReadDeadline(t *testing.T) {
	read := func(w http.ResponseWriter, r *http.Request) {
		if _, err := io.ReadAll(r.Body); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		_, _ = io.WriteString(w, "ok")
	}
	slow := extend(discard, func(r *http.Request) (time.Duration, time.Duration) {
		if r.URL.Path == "/slow" {
			return 5 * time.Second, 5 * time.Second
		}
		return 0, 0
	})
	ln := listen(t)
	srv := New("", slow(http.HandlerFunc(read)), discard)
	srv.ReadTimeout = 300 * time.Millisecond
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = Serve(ctx, srv, ln, time.Second, discard) }()

	post := func(path string) (int, error) {
		req, _ := http.NewRequest("POST", "http://"+ln.Addr().String()+path, &slowBody{n: 8, pause: 100 * time.Millisecond})
		req.ContentLength = 8
		res, err := (&http.Client{Transport: &http.Transport{DisableKeepAlives: true}}).Do(req)
		if err != nil {
			return 0, err
		}
		res.Body.Close()
		return res.StatusCode, nil
	}
	if code, err := post("/slow"); err != nil || code != 200 {
		t.Fatalf("extended route: %d %v", code, err)
	}
	if code, err := post("/other"); err == nil && code == 200 {
		t.Fatal("a body slower than ReadTimeout was read on a route without an extension")
	}
}
