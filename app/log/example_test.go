package log_test

import (
	"log/slog"

	gnuilog "gonativeui/app/log"
)

// Logging for an app: a slog logger that writes to standard error and to a
// size-rotated file in the log directory, plus a record of crashes. Records
// are copied into a bounded buffer and written by a goroutine, so a slow disk
// never stalls the caller. Logging is for events, never for frames.
func Example() {
	logger, err := gnuilog.Setup(gnuilog.Options{ID: "dev.example.app"})
	if err != nil {
		// the logger still works, on standard error
		logger.Warn("log file unavailable", "err", err)
	}
	slog.SetDefault(logger) // or Options.SetDefault
	defer gnuilog.Close()   // flushes; call at quit

	if c, ok := gnuilog.PreviousCrash(); ok {
		logger.Warn("the last run crashed", "at", c.Time, "file", c.Path) // offer to report c.Text
	}
	logger.Info("started", "version", "1.2.0")
}
