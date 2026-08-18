package app

import (
	"context"
	"errors"
	"net/http"
	"time"
)

func (s *Service) Shutdown(ctx context.Context) error {
	s.accepting.Store(false)
	s.stopWorkerIntake()
	s.cancelManagedScanForShutdown()

	var shutdownErr error
	if s.server != nil {
		if err := s.server.Shutdown(ctx); err != nil && !errors.Is(err, http.ErrServerClosed) {
			shutdownErr = err
		}
	}
	s.closeWatcher()
	s.cancelNonCriticalActiveJobs()

	wait := time.NewTicker(100 * time.Millisecond)
	defer wait.Stop()
	for s.hasCriticalPhase() {
		select {
		case <-ctx.Done():
			if shutdownErr == nil {
				shutdownErr = ctx.Err()
			}
			goto cancelWorkers
		case <-wait.C:
		}
	}

cancelWorkers:
	s.cancelAllActiveJobs()
	s.cancelWorker()
	done := make(chan struct{})
	go func() {
		s.workers.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-ctx.Done():
		if shutdownErr == nil {
			shutdownErr = ctx.Err()
		}
	}

	if err := s.db.Close(); err != nil && shutdownErr == nil {
		shutdownErr = err
	}
	if s.logSink != nil {
		if err := s.logSink.Close(); err != nil && shutdownErr == nil {
			shutdownErr = err
		}
		s.logSink = nil
	}
	return shutdownErr
}
