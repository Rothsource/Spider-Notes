// Command app runs the Mynotes server and its maintenance subcommands.
//
//	app [serve]                     start the web server, scheduler and Telegram bot
//	app backup [--out PATH]         write a consistent copy of the database (VACUUM INTO)
//	app healthcheck                 exit 0 if /healthz responds OK (for Docker HEALTHCHECK)
//	app version                     print the version
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/sovannroth/mynotes"
	"github.com/sovannroth/mynotes/internal/api"
	"github.com/sovannroth/mynotes/internal/auth"
	"github.com/sovannroth/mynotes/internal/config"
	"github.com/sovannroth/mynotes/internal/scheduler"
	"github.com/sovannroth/mynotes/internal/service"
	"github.com/sovannroth/mynotes/internal/store/sqlite"
	"github.com/sovannroth/mynotes/internal/telegram"
	"github.com/sovannroth/mynotes/internal/web"
)

// version is set at build time with -ldflags "-X main.version=...".
var version = "dev"

func main() {
	cmd := "serve"
	args := os.Args[1:]
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		cmd, args = args[0], args[1:]
	}
	var err error
	switch cmd {
	case "serve":
		err = serve()
	case "backup":
		err = backup(args)
	case "healthcheck":
		err = healthcheck()
	case "version":
		fmt.Println(version)
	default:
		err = fmt.Errorf("unknown command %q (use serve, backup, healthcheck, version)", cmd)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func setupLogging(level string) {
	var lvl slog.Level
	if err := lvl.UnmarshalText([]byte(level)); err != nil {
		lvl = slog.LevelInfo
	}
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: lvl})))
}

func serve() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	if err := cfg.ValidateForServe(); err != nil {
		return err
	}
	setupLogging(cfg.LogLevel)
	slog.Info("starting mynotes", "version", version, "listen", cfg.Listen, "db", cfg.DBPath, "timezone", cfg.Timezone)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	db, err := sqlite.Open(ctx, cfg.DBPath)
	if err != nil {
		return fmt.Errorf("open database: %w", err)
	}
	defer db.Close()

	am := auth.NewManager(db, cfg.SessionSecret, cfg.SessionIdleTimeout, cfg.TrustProxy)
	if err := am.EnsureAdmin(ctx, cfg.AdminUsername, cfg.AdminPassword); err != nil {
		return err
	}

	svc := service.New(service.Options{Store: db, Location: cfg.Location, ExportDir: cfg.ExportDir,
		EnvTelegramTok: cfg.TelegramToken, EnvTelegramID: cfg.TelegramChatID})

	var wg sync.WaitGroup
	bot := telegram.New(svc, nil)
	bot.InstallLogger()
	svc.SetNotifier(bot)
	svc.OnTelegramChange(bot.Configure)
	wg.Add(1)
	go func() { defer wg.Done(); bot.Run(ctx) }()
	set, err := svc.Settings(ctx)
	if err != nil {
		return err
	}
	bot.Configure(set.TelegramToken, set.TelegramChatID)

	wg.Add(1)
	go func() { defer wg.Done(); scheduler.Run(ctx, svc, cfg.SchedulerInterval) }()

	ui, err := web.New(svc, am, bot, mynotes.OpenAPI, version)
	if err != nil {
		return err
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		if err := db.Ping(r.Context()); err != nil {
			http.Error(w, "db unavailable", http.StatusServiceUnavailable)
			return
		}
		_, _ = w.Write([]byte("ok"))
	})
	api.Register(mux, svc, am)
	ui.Register(mux)

	srv := &http.Server{
		Addr:              cfg.Listen,
		Handler:           logRequests(web.SecurityHeaders(mux)),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      60 * time.Second,
		IdleTimeout:       120 * time.Second,
	}
	errCh := make(chan error, 1)
	go func() {
		slog.Info("http server listening", "addr", cfg.Listen)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()

	select {
	case <-ctx.Done():
		slog.Info("shutting down")
	case err := <-errCh:
		stop()
		wg.Wait()
		return err
	}
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		slog.Error("http shutdown", "err", err)
	}
	wg.Wait()
	slog.Info("stopped")
	return nil
}

type statusWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusWriter) WriteHeader(code int) {
	w.status = code
	w.ResponseWriter.WriteHeader(code)
}

// Unwrap lets http.ResponseController reach the underlying writer.
func (w *statusWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

func logRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		sw := &statusWriter{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(sw, r)
		if r.URL.Path == "/healthz" || strings.HasPrefix(r.URL.Path, "/static/") {
			return
		}
		// Only the path is logged: query strings may contain search text.
		slog.Info("http", "method", r.Method, "path", r.URL.Path, "status", sw.status, "ms", time.Since(start).Milliseconds())
	})
}

func backup(args []string) error {
	fs := flag.NewFlagSet("backup", flag.ContinueOnError)
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	def := fmt.Sprintf("/data/backup-%s.db", time.Now().In(cfg.Location).Format("2006-01-02"))
	out := fs.String("out", def, "output file (must not exist)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if _, err := os.Stat(*out); err == nil {
		return fmt.Errorf("%s already exists", *out)
	}
	ctx := context.Background()
	db, err := sqlite.Open(ctx, cfg.DBPath)
	if err != nil {
		return err
	}
	defer db.Close()
	if err := db.Backup(ctx, *out); err != nil {
		return err
	}
	fmt.Println("backup written to", *out)
	return nil
}

func healthcheck() error {
	listen := os.Getenv("APP_LISTEN")
	if listen == "" {
		listen = ":8080"
	}
	host, port, err := net.SplitHostPort(listen)
	if err != nil {
		return err
	}
	if host == "" || host == "0.0.0.0" || host == "::" {
		host = "127.0.0.1"
	}
	c := &http.Client{Timeout: 3 * time.Second}
	resp, err := c.Get("http://" + net.JoinHostPort(host, port) + "/healthz")
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("status %d", resp.StatusCode)
	}
	return nil
}
