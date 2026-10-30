package server

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	wc "github.com/WalletConnect/walletconnect-go"
	"github.com/WalletConnect/walletconnect-go/storage"
)

// Config is what both demo apps take from flags and the environment.
type Config struct {
	Name     string
	Debug    bool // trace every action and relay message, see Options.Trace
	Addr     string
	DataDir  string
	RelayURL string
	Metadata wc.Metadata
}

// App is a running SDK client with its storage.
type App struct {
	Client    *wc.Client
	DB        *storage.Bolt
	Log       *slog.Logger
	Ctx       context.Context
	TraceFile string
}

// Start opens the Bolt store in DataDir and starts an SDK client on it.
// WC_PROJECT_ID must be set.
func Start(cfg Config) (*App, func(), error) {
	level := slog.LevelInfo
	if cfg.Debug {
		level = slog.LevelDebug
	}
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level})).With("app", cfg.Name)
	if err := os.MkdirAll(cfg.DataDir, 0o700); err != nil {
		return nil, nil, err
	}
	db, err := storage.OpenBolt(filepath.Join(cfg.DataDir, "walletconnect.db"))
	if err != nil {
		return nil, nil, err
	}
	traceFile := filepath.Join(cfg.DataDir, "walletconnect.trace.jsonl")
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	client, err := wc.New(ctx, wc.Options{
		ProjectID: os.Getenv("WC_PROJECT_ID"),
		RelayURL:  cfg.RelayURL,
		Storage:   db,
		Logger:    log,
		Trace:     cfg.Debug,
		TraceFile: traceFile,
		Metadata:  cfg.Metadata,
	})
	if err != nil {
		stop()
		db.Close()
		return nil, nil, err
	}
	closeAll := func() {
		client.Close()
		db.Close()
		stop()
	}
	return &App{Client: client, DB: db, Log: log, Ctx: ctx, TraceFile: traceFile}, closeAll, nil
}

// Serve runs the HTTP server until the app context ends.
func Serve(app *App, addr string, h http.Handler) error {
	srv := &http.Server{Addr: addr, Handler: h, ReadHeaderTimeout: 10 * time.Second}
	go func() {
		<-app.Ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdown)
	}()
	app.Log.Info("listening", "url", "http://localhost"+addr)
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}
