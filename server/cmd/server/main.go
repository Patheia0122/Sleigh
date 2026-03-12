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

	"sleigh-runtime/server/internal/config"
	serverhttp "sleigh-runtime/server/internal/http"
	"sleigh-runtime/server/internal/monitor"
	"sleigh-runtime/server/internal/sandbox"
	dockerbackend "sleigh-runtime/server/internal/sandbox/docker"
	sqlitestore "sleigh-runtime/server/internal/store/sqlite"
	"sleigh-runtime/server/internal/telemetry"
)

func main() {
	cfg := config.FromEnv()
	backend := dockerbackend.NewBackend(cfg.ImagePullTimeoutSeconds)
	store, err := sqlitestore.Open(cfg.DBPath)
	if err != nil {
		log.Fatalf("init sqlite store failed: %v", err)
	}
	defer func() {
		if closeErr := store.Close(); closeErr != nil {
			log.Printf("close sqlite store failed: %v", closeErr)
		}
	}()

	monitorService := monitor.NewService(nil)
	tracer, shutdownOTEL, err := telemetry.InitOTEL(context.Background(), cfg.OTELEndpoint, cfg.Version)
	if err != nil {
		log.Fatalf("init otel failed: %v", err)
	}
	if shutdownOTEL != nil {
		defer func() {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if err := shutdownOTEL(ctx); err != nil {
				log.Printf("shutdown otel failed: %v", err)
			}
		}()
		log.Printf("otel tracing enabled: endpoint=%s", cfg.OTELEndpoint)
	} else {
		log.Printf("otel tracing disabled")
	}
	service := sandbox.NewService(backend, store, nil, sandbox.Policy{
		MinExpandMB:                cfg.MinExpandMB,
		MaxExpandMB:                cfg.MaxExpandMB,
		MaxExpandStepMB:            cfg.MaxExpandStepMB,
		ExecTTLDays:                cfg.ExecTTLDays,
		ExecCleanupIntervalSeconds: cfg.ExecCleanupIntervalSeconds,
		MountAllowedRoot:           cfg.MountAllowedRoot,
		EnvironmentAllowedRoot:     cfg.EnvironmentAllowedRoot,
		WarmPoolSize:               cfg.WarmPoolSize,
		WarmPoolImage:              cfg.WarmPoolImage,
		WarmPoolMemoryMB:           cfg.WarmPoolMemoryMB,
		SnapshotRootDir:            cfg.SnapshotRootDir,
		CursorTokenSecret:          cfg.CursorTokenSecret,
		CursorTokenTTLSeconds:      cfg.CursorTokenTTLSeconds,
		SandboxIdleTTLDays:         cfg.SandboxIdleTTLDays,
	}, tracer)
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
	if cfg.ExecCleanupIntervalSeconds > 0 {
		service.StartIdleCleanupLoop(
			ctx,
			time.Duration(cfg.ExecCleanupIntervalSeconds)*time.Second,
			func(result sandbox.IdleCleanupResult, err error) {
				if err != nil {
					log.Printf("periodic idle sandbox cleanup failed: %v", err)
					return
				}
				log.Printf(
					"periodic idle sandbox cleanup completed: deleted_rows=%d before=%s",
					result.DeletedRows,
					result.Before,
				)
			},
		)
	}
	if cfg.ExecCleanupIntervalSeconds > 0 {
		service.StartImageCleanupLoop(
			ctx,
			time.Duration(cfg.ExecCleanupIntervalSeconds)*time.Second,
			func(result sandbox.ImageCleanupResult, err error) {
				if err != nil {
					log.Printf("periodic image cleanup failed: %v", err)
					return
				}
				log.Printf(
					"periodic image cleanup completed: scanned=%d deleted=%d",
					result.Scanned,
					result.Deleted,
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
