package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os/signal"
	"syscall"
	"time"

	"agent-heavyworks-runtime/server/internal/config"
	serverhttp "agent-heavyworks-runtime/server/internal/http"
	"agent-heavyworks-runtime/server/internal/monitor"
	"agent-heavyworks-runtime/server/internal/notifier"
	"agent-heavyworks-runtime/server/internal/sandbox"
	dockerbackend "agent-heavyworks-runtime/server/internal/sandbox/docker"
	sqlitestore "agent-heavyworks-runtime/server/internal/store/sqlite"
)

func main() {
	cfg := config.FromEnv()
	backend := dockerbackend.NewBackend()
	store, err := sqlitestore.Open(cfg.DBPath)
	if err != nil {
		log.Fatalf("init sqlite store failed: %v", err)
	}
	defer func() {
		if closeErr := store.Close(); closeErr != nil {
			log.Printf("close sqlite store failed: %v", closeErr)
		}
	}()

	reporter := notifier.BuildReporter(cfg.SessionManagerEventURL, notifier.RetryOptions{
		MaxRetries:     cfg.EventRetryMax,
		InitialBackoff: time.Duration(cfg.EventRetryInitialMS) * time.Millisecond,
		MaxBackoff:     time.Duration(cfg.EventRetryMaxMS) * time.Millisecond,
		QueueSize:      cfg.EventQueueSize,
	})
	monitorService := monitor.NewService(reporter)
	service := sandbox.NewService(backend, store, reporter, sandbox.Policy{
		MinExpandMB:                cfg.MinExpandMB,
		MaxExpandMB:                cfg.MaxExpandMB,
		MaxExpandStepMB:            cfg.MaxExpandStepMB,
		ExecTTLDays:                cfg.ExecTTLDays,
		ExecCleanupIntervalSeconds: cfg.ExecCleanupIntervalSeconds,
		MountAllowedRoot:           cfg.MountAllowedRoot,
		WarmPoolSize:               cfg.WarmPoolSize,
		WarmPoolImage:              cfg.WarmPoolImage,
		WarmPoolMemoryMB:           cfg.WarmPoolMemoryMB,
		SnapshotRootDir:            cfg.SnapshotRootDir,
		CursorTokenSecret:          cfg.CursorTokenSecret,
		CursorTokenTTLSeconds:      cfg.CursorTokenTTLSeconds,
	})
	if _, err := service.RefillWarmPool(context.Background()); err != nil {
		log.Printf("warm pool refill on startup failed: %v", err)
	}
	handler := serverhttp.NewHandler(cfg, service, monitorService)

	server := &http.Server{
		Addr:         cfg.HTTPAddr,
		Handler:      handler,
		ReadTimeout:  cfg.ReadTimeout,
		WriteTimeout: cfg.WriteTimeout,
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	if cfg.ExecCleanupIntervalSeconds > 0 {
		service.StartExecCleanupLoop(
			ctx,
			time.Duration(cfg.ExecCleanupIntervalSeconds)*time.Second,
			func(result sandbox.CleanupResult, err error) {
				if err != nil {
					log.Printf("periodic exec cleanup failed: %v", err)
					return
				}
				log.Printf(
					"periodic exec cleanup completed: deleted_rows=%d before=%s",
					result.DeletedRows,
					result.Before,
				)
			},
		)
	}

	errChan := make(chan error, 1)
	go func() {
		printStartupLogo()
		log.Printf("server starting on %s (sandbox backend: %s)", cfg.HTTPAddr, backend.Kind())
		if serveErr := server.ListenAndServe(); serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
			errChan <- serveErr
		}
		close(errChan)
	}()

	select {
	case <-ctx.Done():
		log.Printf("shutdown signal received")
	case err := <-errChan:
		if err != nil {
			log.Fatalf("server failed: %v", err)
		}
		return
	}

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
	defer shutdownCancel()
	if err := server.Shutdown(shutdownCtx); err != nil {
		log.Fatalf("graceful shutdown failed: %v", err)
	}

	log.Printf("server stopped")
}

func printStartupLogo() {
	fmt.Print(`
   _____ _      _       _       
  / ____| |    (_)     | |      
 | (___ | | ___ _  __ _| |__    
  \___ \| |/ _ \ |/ _` + "`" + ` | '_ \   
  ____) | |  __/ | (_| | | | |  
 |_____/|_|\___|_|\__, |_| |_|  
                   __/ |        
                  |___/         

Sleigh - Agent-native elastic sandbox runtime
`)
}
