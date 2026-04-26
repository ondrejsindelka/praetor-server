package main

import (
	"context"
	"errors"
	"flag"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/peer"
	"google.golang.org/grpc/status"

	praetorv1 "github.com/ondrejsindelka/praetor-proto/gen/go/praetor/v1"

	"github.com/ondrejsindelka/praetor-server/internal/agent"
	"github.com/ondrejsindelka/praetor-server/internal/api"
	"github.com/ondrejsindelka/praetor-server/internal/ca"
	"github.com/ondrejsindelka/praetor-server/internal/command"
	"github.com/ondrejsindelka/praetor-server/internal/config"
	"github.com/ondrejsindelka/praetor-server/internal/configpush"
	"github.com/ondrejsindelka/praetor-server/internal/db"
	"github.com/ondrejsindelka/praetor-server/internal/db/store"
	"github.com/ondrejsindelka/praetor-server/internal/enrollment"
	"github.com/ondrejsindelka/praetor-server/internal/staleness"
	lokiwriter "github.com/ondrejsindelka/praetor-server/internal/storage/loki"
	vmwriter "github.com/ondrejsindelka/praetor-server/internal/storage/victoriametrics"
	"github.com/ondrejsindelka/praetor-server/internal/stream"
)

var version = "dev"

func main() {
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "migrate":
			runMigrate(os.Args[2:])
			return
		case "token":
			runToken(os.Args[2:])
			return
		case "config":
			runConfigCmd(os.Args[2:])
			return
		}
	}

	cfgPath := flag.String("config", "/etc/praetor/server.yaml", "path to server config file")
	flag.Parse()

	logger := slog.New(slog.NewJSONHandler(os.Stderr, nil))
	logger.Info("starting praetor-server", "version", version)

	cfg, err := config.Load(*cfgPath)
	if err != nil {
		logger.Error("failed to load config", "err", err)
		os.Exit(1)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	pool, err := db.Connect(ctx, cfg.PostgresDSN)
	if err != nil {
		logger.Error("failed to connect to postgres", "err", err)
		os.Exit(1)
	}
	defer db.Close(pool)
	logger.Info("postgres connected")

	go staleness.Run(ctx, pool, logger, cfg.AlertWebhookURL)

	serverCA, err := ca.New(cfg.DataDir, logger, cfg.GRPCServerDNSNames)
	if err != nil {
		logger.Error("failed to initialize CA", "err", err)
		os.Exit(1)
	}

	vmWriter := vmwriter.New(cfg.VictoriaMetricsURL, logger)
	lokiWriter := lokiwriter.New(cfg.LokiURL, logger)

	registry := stream.NewRegistry()
	configStore := store.NewConfigStore(pool)
	pushSvc := configpush.New(configStore, registry, logger)
	commandStore := store.NewCommandStore(pool)
	broker := command.NewBroker(commandStore, stream.NewRegistryBrokerAdapter(registry), logger)
	secEventStore := store.NewSecurityEventStore(pool)
	connectHandler := stream.NewHandler(registry, store.NewHostStore(pool), vmWriter, lokiWriter, pushSvc, broker, secEventStore, logger)
	enrollSvc := enrollment.New(pool, serverCA, logger)
	agentSvc := agent.New(enrollSvc, connectHandler)

	grpcServer := grpc.NewServer(
		grpc.Creds(credentials.NewTLS(serverCA.ServerTLSConfig())),
		grpc.UnaryInterceptor(mtlsEnforcer),
		grpc.StreamInterceptor(mtlsStreamEnforcer),
	)
	praetorv1.RegisterAgentServiceServer(grpcServer, agentSvc)

	lis, err := net.Listen("tcp", cfg.GRPCListen)
	if err != nil {
		logger.Error("failed to listen", "addr", cfg.GRPCListen, "err", err)
		os.Exit(1)
	}

	go func() {
		logger.Info("gRPC server listening", "addr", cfg.GRPCListen)
		if err := grpcServer.Serve(lis); err != nil {
			logger.Error("gRPC server error", "err", err)
		}
	}()

	apiHandler := api.NewHandler(
		store.NewHostStore(pool),
		store.NewTokenStore(pool),
		broker,
		commandStore,
		secEventStore,
		cfg.APIKey,
		cfg.OrgID,
		logger,
	)
	httpServer := &http.Server{
		Addr:    cfg.HTTPListen,
		Handler: apiHandler.Routes(),
	}
	go func() {
		logger.Info("HTTP API listening", "addr", cfg.HTTPListen)
		if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("HTTP API error", "err", err)
		}
	}()

	<-ctx.Done()
	logger.Info("shutting down praetor-server")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	stopped := make(chan struct{})
	go func() {
		grpcServer.GracefulStop()
		close(stopped)
	}()
	select {
	case <-stopped:
		logger.Info("gRPC server stopped gracefully")
	case <-shutdownCtx.Done():
		logger.Warn("gRPC server shutdown timed out, forcing stop")
		grpcServer.Stop()
	}
	if err := httpServer.Shutdown(shutdownCtx); err != nil {
		logger.Warn("HTTP server shutdown error", "err", err)
	}
}

// mtlsEnforcer is a unary interceptor that requires a verified client certificate
// for all RPCs except Enroll (which is the bootstrap call, no cert yet).
func mtlsEnforcer(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
	if info.FullMethod == praetorv1.AgentService_Enroll_FullMethodName {
		return handler(ctx, req)
	}
	p, ok := peer.FromContext(ctx)
	if !ok {
		return nil, status.Errorf(codes.Unauthenticated, "no peer info")
	}
	tlsInfo, ok := p.AuthInfo.(credentials.TLSInfo)
	if !ok || len(tlsInfo.State.VerifiedChains) == 0 {
		return nil, status.Errorf(codes.Unauthenticated, "client certificate required for %s", info.FullMethod)
	}
	return handler(ctx, req)
}

// mtlsStreamEnforcer is a stream interceptor that requires a verified client certificate.
// Unlike Enroll (unary), Connect always requires mTLS — there is no bypass.
func mtlsStreamEnforcer(srv any, ss grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
	p, ok := peer.FromContext(ss.Context())
	if !ok {
		return status.Errorf(codes.Unauthenticated, "no peer info")
	}
	tlsInfo, ok := p.AuthInfo.(credentials.TLSInfo)
	if !ok || len(tlsInfo.State.VerifiedChains) == 0 {
		return status.Errorf(codes.Unauthenticated, "client certificate required for streaming RPC %s", info.FullMethod)
	}
	return handler(srv, ss)
}
