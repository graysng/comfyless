package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"comfyless/internal/api"
	"comfyless/internal/comfyui"
	"comfyless/internal/config"
	"comfyless/internal/dispatcher"
	"comfyless/internal/queue"
	"comfyless/internal/worker"

	"github.com/gin-gonic/gin"
	"github.com/sirupsen/logrus"
)

func main() {
	// Load configuration
	cfg := config.Load()

	// Configure global logger
	config.ConfigureGlobalLogger()

	// Validate worker manager type
	factory := worker.NewFactory()
	if !factory.ValidateWorkerManagerType(cfg.WorkerManager) {
		logrus.Fatalf("Unsupported worker manager type: %s, supported types: %s",
			cfg.WorkerManager, strings.Join(factory.GetSupportedTypes(), ", "))
	}

	// Initialize components
	qm := queue.NewManager(cfg.Redis)

	// Create ComfyUI client
	comfyClient := comfyui.NewClient()

	// Create worker manager using factory
	wm, err := factory.CreateWorkerManager(cfg, comfyClient)
	if err != nil {
		logrus.Fatalf("Failed to create worker manager: %v", err)
	}

	// Create task dispatcher
	taskDispatcher := dispatcher.NewDispatcher(qm, wm, comfyClient)

	logrus.Infof("Using worker manager type: %s", cfg.WorkerManager)
	if cfg.WorkerManager == "docker" {
		logrus.Info("Docker Provider: Managing workers using Docker containers")
	} else if cfg.WorkerManager == "novita" {
		logrus.Info("Novita Provider: Managing workers using Novita AI cloud service")
	}

	// Start all components
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Start worker manager
	go func() {
		if err := wm.Start(ctx); err != nil {
			logrus.WithError(err).Error("Failed to start worker manager")
		}
	}()

	// Start queue manager
	go func() {
		if err := qm.Start(ctx); err != nil {
			logrus.WithError(err).Error("Failed to start queue manager")
		}
	}()
	logrus.Info("Starting queue manager")

	// Start task dispatcher
	go func() {
		if err := taskDispatcher.Start(ctx); err != nil {
			logrus.WithError(err).Error("Failed to start task dispatcher")
		}
	}()
	logrus.Info("Starting task dispatcher")

	// Start HTTP server
	router := gin.Default()
	apiHandler := api.NewHandler(qm, wm)
	apiHandler.RegisterRoutes(router)

	srv := &http.Server{
		Addr:    fmt.Sprintf(":%d", cfg.Port),
		Handler: router,
	}

	// Start server
	go func() {
		logrus.Infof("Server starting on port %d", cfg.Port)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logrus.Fatalf("Failed to listen: %s\n", err)
		}
	}()

	// Wait for interrupt signal
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	logrus.Info("Server shutting down...")

	// Graceful shutdown
	ctxShutdown, cancelShutdown := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancelShutdown()

	if err := srv.Shutdown(ctxShutdown); err != nil {
		logrus.Fatal("Server forced to shutdown:", err)
	}

	logrus.Info("Server exited")
}
