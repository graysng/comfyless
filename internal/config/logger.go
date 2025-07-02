package config

import (
	"github.com/sirupsen/logrus"
)

// NewLogger creates a new logger instance with consistent formatting
func NewLogger() *logrus.Logger {
	logger := logrus.New()
	logger.SetLevel(logrus.InfoLevel)
	logger.SetFormatter(&logrus.JSONFormatter{
		TimestampFormat: "2006-01-02T15:04:05Z07:00",
	})
	return logger
}

// ConfigureGlobalLogger configures the global logrus instance
func ConfigureGlobalLogger() {
	logrus.SetLevel(logrus.InfoLevel)
	logrus.SetFormatter(&logrus.JSONFormatter{
		TimestampFormat: "2006-01-02T15:04:05Z07:00",
	})
}
