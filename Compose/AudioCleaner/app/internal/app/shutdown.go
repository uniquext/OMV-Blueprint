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
		shutdownServer := s.shutdownServer
		if shutdownServer == nil {
			shutdownServer = s.server.Shutdown
		}
		if err := shutdownServer(ctx); err != nil && !errors.Is(err, http.ErrServerClosed) {
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

	closeRepository := s.closeRepository
	if closeRepository == nil {
		closeRepository = s.db.Close
	}
	if err := closeRepository(); err != nil && shutdownErr == nil {
		shutdownErr = err
	}
	if s.logCloser != nil {
		if err := s.logCloser(); err != nil && shutdownErr == nil {
			shutdownErr = err
		}
		s.logCloser = nil
	}
	return shutdownErr
}
