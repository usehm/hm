package app

import (
	"context"
	"os"
	"time"
)

func sleepMs(n int) { time.Sleep(time.Duration(n) * time.Millisecond) }

func ctxTimeout(d time.Duration) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), d)
}

func readFileErr(p string) (string, error) {
	b, err := os.ReadFile(p)
	return string(b), err
}
