package main

import (
	"crypto/rand"
	"encoding/hex"
	"sync"
	"time"
)

// deployTTL is how long a finished deploy stays queryable via /redeploy/status.
const deployTTL = time.Hour

// ServiceResult is the outcome of one service's rollout within a deploy.
type ServiceResult struct {
	State string `json:"state"` // running|ok|failed
	Error string `json:"error,omitempty"`
}

// Deploy tracks every service redeployed by a single /redeploy call.
type Deploy struct {
	ID       string
	Started  time.Time
	finished time.Time
	services map[string]ServiceResult // keyed by service name
}

// Deploys is an in-memory registry of deploys, keyed by ID. Single instance only.
type Deploys struct {
	mu   sync.Mutex
	byID map[string]*Deploy
}

func NewDeploys() *Deploys {
	return &Deploys{byID: map[string]*Deploy{}}
}

// Start registers a new deploy with every service in the running state.
// Services that could not be triggered are recorded as failed straight away.
func (d *Deploys) Start(names []string, triggerErrors map[string]string) *Deploy {
	dep := &Deploy{ID: newID(), Started: time.Now(), services: map[string]ServiceResult{}}
	for _, n := range names {
		if e, bad := triggerErrors[n]; bad {
			dep.services[n] = ServiceResult{State: "failed", Error: "redeploy failed: " + e}
		} else {
			dep.services[n] = ServiceResult{State: "running"}
		}
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	d.sweepLocked()
	d.byID[dep.ID] = dep
	return dep
}

// Finish records the outcome of one service's rollout (err == nil means success).
func (d *Deploys) Finish(dep *Deploy, name string, err error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if err != nil {
		dep.services[name] = ServiceResult{State: "failed", Error: err.Error()}
	} else {
		dep.services[name] = ServiceResult{State: "ok"}
	}
	if dep.doneLocked() && dep.finished.IsZero() {
		dep.finished = time.Now()
	}
}

// Get returns a snapshot of the deploy's services and whether every service has finished.
func (d *Deploys) Get(id string) (services map[string]ServiceResult, done, found bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	dep, ok := d.byID[id]
	if !ok {
		return nil, false, false
	}
	services = make(map[string]ServiceResult, len(dep.services))
	for k, v := range dep.services {
		services[k] = v
	}
	return services, dep.doneLocked(), true
}

func (dep *Deploy) doneLocked() bool {
	for _, r := range dep.services {
		if r.State == "running" {
			return false
		}
	}
	return true
}

// sweepLocked drops deploys that finished more than deployTTL ago.
func (d *Deploys) sweepLocked() {
	for id, dep := range d.byID {
		if !dep.finished.IsZero() && time.Since(dep.finished) > deployTTL {
			delete(d.byID, id)
		}
	}
}

func newID() string {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		panic(err) // crypto/rand never fails on supported platforms
	}
	return hex.EncodeToString(b)
}
