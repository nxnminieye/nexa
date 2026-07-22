package jobapp

import "context"

type HealthStatus string

const (
	HealthReady    HealthStatus = "ready"
	HealthRunning  HealthStatus = "running"
	HealthDegraded HealthStatus = "degraded"
	HealthStopped  HealthStatus = "stopped"
)

type Health struct {
	Status        HealthStatus
	ActiveRuns    int
	LastErrorCode ErrorCode
}

func (s *Scheduler) Health(ctx context.Context) Health {
	s.mu.Lock()
	defer s.mu.Unlock()
	result := Health{Status: s.healthStatus, ActiveRuns: s.activeRuns, LastErrorCode: s.lastErrorCode}
	if ctx == nil || ctx.Err() != nil {
		result.LastErrorCode = CodeCanceled
	}
	return result
}

func (s *Scheduler) changeActive(delta int) {
	s.mu.Lock()
	s.activeRuns += delta
	s.mu.Unlock()
}

func (s *Scheduler) backgroundFailure(err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return
	}
	s.healthStatus = HealthDegraded
	s.lastErrorCode = CodeOf(err)
}
