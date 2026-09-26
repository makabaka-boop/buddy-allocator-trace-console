package buddy

import (
	"fmt"
	"time"
)

func timeNow() int64 { return time.Now().UnixNano() }

func idFor(seed int64, iter, i int) string {
	return fmt.Sprintf("s%d-%d-e%d", seed, iter, i)
}
