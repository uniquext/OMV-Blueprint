package pipeline

import (
	"context"
	"errors"
	"os"
	"time"
)

type StableStat struct {
	Size    int64
	MTimeNS int64
}

func WaitForStableFile(ctx context.Context, path string, quiet time.Duration) (StableStat, error) {
	return waitForStableFile(ctx, path, quiet, stableStat, time.After)
}

func waitForStableFile(ctx context.Context, path string, quiet time.Duration, stat func(string) (StableStat, error), after func(time.Duration) <-chan time.Time) (StableStat, error) {
	select {
	case <-ctx.Done():
		return StableStat{}, ctx.Err()
	default:
	}

	first, err := stat(path)
	if err != nil {
		return StableStat{}, err
	}
	if first.Size <= 0 {
		return StableStat{}, errors.New("file is empty")
	}

	select {
	case <-ctx.Done():
		return StableStat{}, ctx.Err()
	case <-after(quiet):
	}

	second, err := stat(path)
	if err != nil {
		return StableStat{}, err
	}
	if first != second {
		return StableStat{}, errors.New("file changed during quiet period")
	}
	select {
	case <-ctx.Done():
		return StableStat{}, ctx.Err()
	default:
	}
	return second, nil
}

func stableStat(path string) (StableStat, error) {
	info, err := os.Stat(path)
	if err != nil {
		return StableStat{}, err
	}
	return StableStat{Size: info.Size(), MTimeNS: info.ModTime().UnixNano()}, nil
}
