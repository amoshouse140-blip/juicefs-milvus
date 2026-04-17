package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/juicedata/juicefs-milvus/milvus/bridge/internal/httpserver"
	"github.com/juicedata/juicefs-milvus/milvus/bridge/internal/service"
)

func main() {
	listenAddr := getenv("BRIDGE_LISTEN_ADDR", ":19531")
	milvusAddr := getenv("MILVUS_ADDR", "127.0.0.1:19530")

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	svc, err := service.New(ctx, milvusAddr)
	if err != nil {
		log.Fatalf("create bridge service: %v", err)
	}
	defer func() {
		closeCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := svc.Close(closeCtx); err != nil {
			log.Printf("close bridge service: %v", err)
		}
	}()

	server := &http.Server{
		Addr:              listenAddr,
		Handler:           httpserver.NewHandler(svc),
		ReadHeaderTimeout: 5 * time.Second,
	}

	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdownCtx)
	}()

	log.Printf("vectorbucket milvus bridge listening on %s -> %s", listenAddr, milvusAddr)
	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatalf("serve bridge: %v", err)
	}
}

func getenv(key string, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}
