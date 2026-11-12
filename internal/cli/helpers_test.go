package cli

import "time"

func farFuture() time.Time { return time.Now().Add(24 * time.Hour) }
