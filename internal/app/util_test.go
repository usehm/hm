package app

import "time"

func sleepMs(n int) { time.Sleep(time.Duration(n) * time.Millisecond) }
