// Command swarmpit-ci-deploy redeploys Docker Swarm services through the
// Swarmpit API and reports whether the rollout converged.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"
)

// version is injected at build time via -ldflags "-X main.version=...".
var version = "dev"

type App struct {
	key     string
	webhook string
	debug   bool
	sp      *Swarmpit
	watch   WatchOptions
	deploys *Deploys
}

func main() {
	log.Printf("swarmpit-ci-deploy %s", version)
	port := envInt("APP_PORT", 3052)
	debug := os.Getenv("DEBUG") != ""
	webhook := os.Getenv("ALERT_WEBHOOK")
	swarmpitURL := envOr("SWARMPIT_URL", "http://127.0.0.1:888")
	auth := secret("SWARMPIT_AUTH")
	key := secret("APP_KEY")

	if auth == "" {
		log.Fatal("Please get an access token on settings page of Swarmpit panel and provide it (SWARMPIT_AUTH).")
	}

	app := &App{
		key:     key,
		webhook: webhook,
		debug:   debug,
		sp:      NewSwarmpit(swarmpitURL, auth, debug),
		deploys: NewDeploys(),
		watch: WatchOptions{
			Timeout:  time.Duration(envInt("WATCH_TIMEOUT", 300)) * time.Second,
			Settle:   time.Duration(envInt("WATCH_SETTLE", 30)) * time.Second,
			Interval: time.Duration(envInt("WATCH_INTERVAL", 3)) * time.Second,
		},
	}

	log.Printf("APP_CONFIG port=%d debug=%v swarmpit=%q auth=%v key=%v webhook=%v watch=%+v", //nolint:gosec // operator-provided config, %q-quoted
		port, debug, swarmpitURL, auth != "", key != "", webhook != "", app.watch)

	if err := app.sp.Ping(context.Background()); err != nil {
		app.alert("APP_INIT > Api is not working > " + err.Error())
		log.Fatalf("Swarmpit API error. Can't start app: %v", err)
	}
	log.Print("API is working fine")

	if webhook != "" {
		go func() {
			for range time.Tick(5 * time.Minute) {
				if err := app.sp.Ping(context.Background()); err != nil {
					log.Printf("API is not working: %v", err)
					app.alert("APP_INTERVAL > API is not working > " + err.Error())
				}
			}
		}()
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
	mux.HandleFunc("GET /favicon.ico", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
	mux.HandleFunc("GET /redeploy", app.redeploy)
	mux.HandleFunc("GET /redeploy/status", app.status)
	mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) {
		app.reply(w, http.StatusNotFound, "Path not found")
	})

	srv := &http.Server{
		Addr:              ":" + strconv.Itoa(port),
		Handler:           app.log(mux),
		ReadHeaderTimeout: 10 * time.Second,
	}
	log.Printf("server is listening on %d", port)
	if err := srv.ListenAndServe(); err != nil {
		app.alert("SERVER > START_ERROR > " + err.Error())
		log.Fatal(err)
	}
}

// redeploy handles GET /redeploy?key=&name=|id=[&timeout=seconds].
// It triggers the rollouts and returns 202 at once with the deploy ID
// (JSON "deploy" and X-Deploy-ID header) for polling via /redeploy/status.
func (a *App) redeploy(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	if a.key != "" && q.Get("key") != a.key {
		a.reply(w, http.StatusUnauthorized, "please provide correct key")
		return
	}
	name, ids := q.Get("name"), splitList(q.Get("id"))
	if name == "" && len(ids) == 0 {
		a.alert("REQUEST > ID or NAME is missing > " + r.URL.RawQuery)
		a.reply(w, http.StatusBadRequest, "query :id or :name must be provided")
		return
	}

	ctx := r.Context()
	targets := map[string]Service{}
	if name != "" {
		services, err := a.sp.Services(ctx)
		if err != nil {
			a.alert("REQUEST > FETCH_ERROR > " + err.Error())
			a.reply(w, http.StatusBadGateway, err.Error())
			return
		}
		for _, s := range services {
			if s.ServiceName == name {
				targets[s.ID] = s
			}
		}
	}
	for _, id := range ids {
		if _, ok := targets[id]; ok {
			continue
		}
		s, err := a.sp.Service(ctx, id)
		if err != nil {
			log.Printf("service %s: %v", id, err)
			continue
		}
		targets[s.ID] = s
	}
	if len(targets) == 0 {
		a.alert("REQUEST > NO_SERVICES_FOUND > " + r.URL.RawQuery)
		a.reply(w, http.StatusNotFound, "no services found")
		return
	}

	opts := a.watch
	if t, err := strconv.Atoi(q.Get("timeout")); err == nil && t > 0 {
		opts.Timeout = time.Duration(t) * time.Second
	}
	// Trigger every redeploy first so one slow service does not delay the others.
	var started []Service
	var names []string
	triggerErrors := map[string]string{}
	for _, s := range targets {
		names = append(names, s.ServiceName)
		if err := a.sp.Redeploy(ctx, s.ID); err != nil {
			a.alert(fmt.Sprintf("REQUEST > REDEPLOY_ERROR > #%s > %s", s.ServiceName, err))
			log.Printf("redeploy.err %s: %v", s.ServiceName, err)
			triggerErrors[s.ServiceName] = err.Error()
			continue
		}
		started = append(started, s)
	}
	since := time.Now()
	if len(started) == 0 {
		a.reply(w, http.StatusBadGateway, "Redeployment failed")
		return
	}
	dep := a.deploys.Start(names, triggerErrors)
	w.Header().Set("X-Deploy-ID", dep.ID)
	extra := map[string]any{"deploy": dep.ID}

	// Watch outcomes in the background; the request never blocks on the rollout.
	for _, s := range started {
		go func(s Service) { //nolint:gosec // deliberately detached from the request context so a client disconnect never stops alerting
			err := Watch(context.Background(), a.sp, s.ID, s.Version, since, opts)
			a.deploys.Finish(dep, s.ServiceName, err)
			if err != nil {
				log.Printf("deploy.failed %s: %v", s.ServiceName, err)
				a.alert(fmt.Sprintf("DEPLOY > FAILED > #%s > %s", s.ServiceName, err))
				return
			}
			log.Printf("deploy.ok %s", s.ServiceName)
			a.alert(fmt.Sprintf("DEPLOY > SUCCESS > #%s", s.ServiceName))
		}(s)
	}

	if len(started) < len(targets) {
		a.replyWith(w, http.StatusBadGateway, fmt.Sprintf("Not every service was redeployed %d/%d", len(started), len(targets)), extra)
		return
	}
	a.replyWith(w, http.StatusAccepted, "", extra)
}

// status handles GET /redeploy/status?key=&deploy=<id> and reports the outcome
// of an earlier /redeploy call: 202 while any service is still rolling out,
// 200 when all converged, 500 when any failed, 404 for an unknown/expired ID.
func (a *App) status(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	if a.key != "" && q.Get("key") != a.key {
		a.reply(w, http.StatusUnauthorized, "please provide correct key")
		return
	}
	id := q.Get("deploy")
	if id == "" {
		a.reply(w, http.StatusBadRequest, "query :deploy must be provided")
		return
	}
	services, done, found := a.deploys.Get(id)
	if !found {
		a.reply(w, http.StatusNotFound, "deploy not found")
		return
	}
	extra := map[string]any{"deploy": id, "done": done, "services": services}
	var failed []string
	for name, res := range services {
		if res.State == "failed" {
			failed = append(failed, name+": "+res.Error)
		}
	}
	sort.Strings(failed)
	switch {
	case !done:
		a.replyWith(w, http.StatusAccepted, "", extra)
	case len(failed) > 0:
		a.replyWith(w, http.StatusInternalServerError, strings.Join(failed, " || "), extra)
	default:
		a.replyWith(w, http.StatusOK, "", extra)
	}
}

func (a *App) reply(w http.ResponseWriter, status int, errMsg string) {
	a.replyWith(w, status, errMsg, nil)
}

func (a *App) replyWith(w http.ResponseWriter, status int, errMsg string, extra map[string]any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	body := map[string]any{"success": errMsg == ""}
	if errMsg != "" {
		body["error"] = errMsg
	}
	for k, v := range extra {
		body[k] = v
	}
	_ = json.NewEncoder(w).Encode(body)
}

func (a *App) log(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if a.debug {
			log.Printf("req %s %q %q %q", r.Method, r.Host, r.URL.Path, r.URL.RawQuery) //nolint:gosec // debug log, %q-quoted
		}
		next.ServeHTTP(w, r)
	})
}

// alert pings ALERT_WEBHOOK with {MESSAGE} replaced by the url-encoded message.
func (a *App) alert(message string) {
	if a.webhook == "" {
		return
	}
	u := strings.ReplaceAll(a.webhook, "{MESSAGE}", url.QueryEscape(message))
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil) //nolint:gosec // ALERT_WEBHOOK is operator-provided by design
	if err != nil {
		log.Printf("webhook: %v", err)
		return
	}
	res, err := http.DefaultClient.Do(req) //nolint:gosec // see above
	if err != nil {
		log.Printf("webhook: %v", err)
		return
	}
	_ = res.Body.Close()
}

/* config helpers */

func envOr(name, def string) string {
	if v := os.Getenv(name); v != "" {
		return v
	}
	return def
}

func envInt(name string, def int) int {
	if v, err := strconv.Atoi(os.Getenv(name)); err == nil && v > 0 {
		return v
	}
	return def
}

// secret reads NAME, then the file named by NAME_CONFIG, then /run/secrets/$NAME_SECRET.
func secret(name string) string {
	if v := os.Getenv(name); v != "" {
		return v
	}
	for _, path := range []string{os.Getenv(name + "_CONFIG"), secretPath(os.Getenv(name + "_SECRET"))} {
		if path == "" {
			continue
		}
		b, err := os.ReadFile(path) //nolint:gosec // path comes from *_CONFIG/*_SECRET env, set by the operator
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			log.Printf("%s: %v", name, err)
		}
		if v := strings.TrimSpace(string(b)); v != "" {
			return v
		}
	}
	return ""
}

func secretPath(name string) string {
	if name == "" {
		return ""
	}
	return "/run/secrets/" + name
}

func splitList(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}
