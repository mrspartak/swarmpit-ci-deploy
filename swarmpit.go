package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"
)

// Service is the subset of Swarmpit's service object we rely on.
type Service struct {
	ID          string `json:"id"`
	ServiceName string `json:"serviceName"`
	Version     int64  `json:"version"`
	Status      struct {
		Tasks struct {
			Running int `json:"running"`
			Total   int `json:"total"`
		} `json:"tasks"`
		Update  string `json:"update"`  // Docker UpdateStatus.State: updating|completed|paused|rollback_started|rollback_paused|rollback_completed
		Message string `json:"message"` // Docker UpdateStatus.Message
	} `json:"status"`
}

// Task is the subset of Swarmpit's task object we rely on.
type Task struct {
	TaskName     string    `json:"taskName"`
	State        string    `json:"state"`
	DesiredState string    `json:"desiredState"`
	CreatedAt    time.Time `json:"createdAt"`
	Status       struct {
		Error string `json:"error"`
	} `json:"status"`
}

type Swarmpit struct {
	base  string
	auth  string
	debug bool
	http  *http.Client
}

func NewSwarmpit(base, auth string, debug bool) *Swarmpit {
	return &Swarmpit{base: base, auth: auth, debug: debug, http: &http.Client{Timeout: 30 * time.Second}}
}

func (s *Swarmpit) do(ctx context.Context, method, path string, out any) error {
	req, err := http.NewRequestWithContext(ctx, method, s.base+path, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", s.auth)
	req.Header.Set("Accept", "application/json")
	res, err := s.http.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(res.Body, 512))
		return fmt.Errorf("%s %s: %s %s", method, path, res.Status, string(body))
	}
	if out == nil {
		return nil
	}
	return json.NewDecoder(res.Body).Decode(out)
}

func (s *Swarmpit) Ping(ctx context.Context) error {
	return s.do(ctx, http.MethodGet, "/api/me", nil)
}

func (s *Swarmpit) Services(ctx context.Context) ([]Service, error) {
	var out []Service
	return out, s.do(ctx, http.MethodGet, "/api/services", &out)
}

func (s *Swarmpit) Service(ctx context.Context, id string) (Service, error) {
	var out Service
	return out, s.do(ctx, http.MethodGet, "/api/services/"+url.PathEscape(id), &out)
}

func (s *Swarmpit) Tasks(ctx context.Context, id string) ([]Task, error) {
	var out []Task
	return out, s.do(ctx, http.MethodGet, "/api/services/"+url.PathEscape(id)+"/tasks", &out)
}

func (s *Swarmpit) Redeploy(ctx context.Context, id string) error {
	return s.do(ctx, http.MethodPost, "/api/services/"+url.PathEscape(id)+"/redeploy", nil)
}
