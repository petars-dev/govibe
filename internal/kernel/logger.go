package kernel

import (
	"os"
	"strings"

	"github.com/sirupsen/logrus"
)

// init sets a safe default configuration early in process startup; the boot overrides it via Setup.
func init() {
	// Default to info unless LOG_LEVEL says otherwise.
	Setup(os.Getenv("LOG_LEVEL"))
}

// Setup configures the global logrus logger.
func Setup(level string) {
	// Formatter: human-readable text with full timestamps.
	logrus.SetFormatter(&logrus.TextFormatter{
		FullTimestamp:   true,
		TimestampFormat: "2006-01-02 15:04:05",
		DisableQuote:    true,
	})

	// Parse level, default to info.
	lvl, err := logrus.ParseLevel(strings.ToLower(strings.TrimSpace(level)))
	if err != nil {
		lvl = logrus.InfoLevel
	}
	logrus.SetLevel(lvl)
}
