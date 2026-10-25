package app

import (
	"context"
	"time"
)

func sleepMs(n int) { time.Sleep(time.Duration(n) * time.Millisecond) }

func ctxTimeout(d time.Duration) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), d)
}
