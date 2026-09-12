package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// fakeSwarmpit serves the handful of endpoints the deployer touches. Its
// service state is scripted per poll so tests can play out a deploy.
type fakeSwarmpit struct {
	mu        sync.Mutex
	polls     int
	script    func(poll int) (update string, running, total int)
	tasks     []Task
	redeploys int32
	version   int64
}

func (f *fakeSwarmpit) service() Service {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.polls++
	update, running, total := f.script(f.polls)
	s := Service{ID: "abc", ServiceName: "web", Version: f.version}
	s.Status.Update = update
	s.Status.Message = "msg"
	s.Status.Tasks.Running = running
	s.Status.Tasks.Total = total
	return s
}

func (f *fakeSwarmpit) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/me", func(_ http.ResponseWriter, _ *http.Request) {})
	mux.HandleFunc("GET /api/services", func(w http.ResponseWriter, _ *http.Request) {
		json.NewEncoder(w).Encode([]Service{f.service()})
	})
	mux.HandleFunc("GET /api/services/abc", func(w http.ResponseWriter, _ *http.Request) {
		json.NewEncoder(w).Encode(f.service())
	})
	mux.HandleFunc("GET /api/services/abc/tasks", func(w http.ResponseWriter, _ *http.Request) {
		json.NewEncoder(w).Encode(f.tasks)
	})
	mux.HandleFunc("POST /api/services/abc/redeploy", func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&f.redeploys, 1)
		f.mu.Lock()
		f.version++
		f.mu.Unlock()
		w.WriteHeader(http.StatusAccepted)
	})
	return mux
}

// newApp wires an App to the fake; the returned func snapshots alerts sent so far.
func newApp(t *testing.T, f *fakeSwarmpit) (*App, func() []string) {
	t.Helper()
	var alerts []string
	var mu sync.Mutex
	hook := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		mu.Lock()
		alerts = append(alerts, r.URL.Query().Get("m"))
		mu.Unlock()
	}))
	sp := httptest.NewServer(f.handler())
	t.Cleanup(func() { hook.Close(); sp.Close() })
	snapshot := func() []string {
		mu.Lock()
		defer mu.Unlock()
		return append([]string(nil), alerts...)
	}
	return &App{
		key:     "k",
		webhook: hook.URL + "/?m={MESSAGE}",
		sp:      NewSwarmpit(sp.URL, "Bearer x", false),
		watch:   WatchOptions{Timeout: 2 * time.Second, Settle: 50 * time.Millisecond, Interval: 10 * time.Millisecond},
	}, snapshot
}

func call(t *testing.T, app *App, query string) (int, map[string]any) {
	t.Helper()
	rec := httptest.NewRecorder()
	app.redeploy(rec, httptest.NewRequest(http.MethodGet, "/redeploy?"+query, nil))
	var body map[string]any
	json.NewDecoder(rec.Body).Decode(&body)
	return rec.Code, body
}

func TestWaitSuccess(t *testing.T) {
	f := &fakeSwarmpit{script: func(p int) (string, int, int) {
		if p < 4 {
			return "updating", 0, 2
		}
		return "completed", 2, 2
	}}
	app, alerts := newApp(t, f)
	code, body := call(t, app, "key=k&name=web&wait=1")
	if code != 200 || body["success"] != true {
		t.Fatalf("got %d %v", code, body)
	}
	if n := atomic.LoadInt32(&f.redeploys); n != 1 {
		t.Fatalf("redeploys=%d", n)
	}
	if !contains(alerts(), "DEPLOY > SUCCESS > #web") {
		t.Fatalf("alerts=%v", alerts())
	}
}

func TestWaitRollbackReportsTaskError(t *testing.T) {
	f := &fakeSwarmpit{script: func(p int) (string, int, int) {
		if p < 3 {
			return "updating", 1, 2
		}
		return "rollback_completed", 2, 2
	}}
	f.tasks = []Task{
		{TaskName: "web.1", State: "failed", CreatedAt: time.Now().Add(time.Minute)},
		{TaskName: "web.old", State: "failed", CreatedAt: time.Now().Add(-time.Hour)},
	}
	f.tasks[0].Status.Error = "task: non-zero exit (1)"
	f.tasks[1].Status.Error = "ancient failure"
	app, alerts := newApp(t, f)
	code, body := call(t, app, "key=k&name=web&wait=1")
	if code != 500 || body["success"] != false {
		t.Fatalf("got %d %v", code, body)
	}
	msg, _ := body["error"].(string)
	if !strings.Contains(msg, "rollback_completed") || !strings.Contains(msg, "non-zero exit") || strings.Contains(msg, "ancient") {
		t.Fatalf("error=%q", msg)
	}
	if !contains(alerts(), "DEPLOY > FAILED > #web") {
		t.Fatalf("alerts=%v", alerts())
	}
}

func TestWaitCrashLoopNeverSettles(t *testing.T) {
	// Update completes but replicas keep flapping: must end in timeout.
	f := &fakeSwarmpit{script: func(p int) (string, int, int) {
		if p%3 == 0 {
			return "completed", 1, 2
		}
		return "completed", 2, 2
	}}
	app, _ := newApp(t, f)
	code, body := call(t, app, "key=k&name=web&wait=1&timeout=1")
	if code != 500 || !strings.Contains(body["error"].(string), "timeout") {
		t.Fatalf("got %d %v", code, body)
	}
}

func TestStaleCompletedIsIgnored(t *testing.T) {
	// Service reports "completed" from an older update before the new one starts.
	f := &fakeSwarmpit{version: 5, script: func(_ int) (string, int, int) {
		return "completed", 2, 2
	}}
	// Redeploy bumps version to 6, so the first poll already counts; force the
	// check by starting the watch with prevVersion equal to the bumped one.
	app, _ := newApp(t, f)
	f.version = 5
	sp := app.sp
	err := Watch(t.Context(), sp, "abc", 6, time.Now(), WatchOptions{Timeout: 200 * time.Millisecond, Settle: 10 * time.Millisecond, Interval: 10 * time.Millisecond})
	if err == nil || !strings.Contains(err.Error(), "timeout") {
		t.Fatalf("expected timeout on stale status, got %v", err)
	}
}

func TestAsyncRepliesImmediatelyAndAlerts(t *testing.T) {
	f := &fakeSwarmpit{script: func(_ int) (string, int, int) { return "paused", 0, 2 }}
	app, alerts := newApp(t, f)
	start := time.Now()
	code, _ := call(t, app, "key=k&name=web")
	if code != 202 || time.Since(start) > 500*time.Millisecond {
		t.Fatalf("code=%d elapsed=%s", code, time.Since(start))
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && !contains(alerts(), "DEPLOY > FAILED > #web") {
		time.Sleep(20 * time.Millisecond)
	}
	if !contains(alerts(), "DEPLOY > FAILED > #web") {
		t.Fatalf("alerts=%v", alerts())
	}
}

func TestAuthAndValidation(t *testing.T) {
	app, _ := newApp(t, &fakeSwarmpit{script: func(int) (string, int, int) { return "", 0, 0 }})
	if code, _ := call(t, app, "key=wrong&name=web"); code != 401 {
		t.Fatalf("code=%d", code)
	}
	if code, _ := call(t, app, "key=k"); code != 400 {
		t.Fatalf("code=%d", code)
	}
	if code, _ := call(t, app, "key=k&name=nope"); code != 404 {
		t.Fatalf("code=%d", code)
	}
}

func TestWebhookEncodesMessage(t *testing.T) {
	var got string
	hook := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) { got = r.URL.RawQuery }))
	defer hook.Close()
	app := &App{webhook: hook.URL + "/?text={MESSAGE}"}
	app.alert("DEPLOY > FAILED > #web > update paused: x & y")
	if q, _ := url.ParseQuery(got); q.Get("text") != "DEPLOY > FAILED > #web > update paused: x & y" {
		t.Fatalf("raw=%q", got)
	}
}

func contains(list []string, prefix string) bool {
	for _, s := range list {
		if strings.HasPrefix(s, prefix) {
			return true
		}
	}
	return false
}
