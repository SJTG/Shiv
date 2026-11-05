// Comando server: arranca la app web de CV Keyword Tailor.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"cvtailor/internal/ai"
	"cvtailor/internal/render"
	"cvtailor/internal/store"
	"cvtailor/internal/web"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func env(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func run() error {
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))

	addr := env("ADDR", "127.0.0.1:"+env("PORT", "8080"))
	if host, _, err := net.SplitHostPort(addr); err == nil {
		if ip := net.ParseIP(host); host != "localhost" && (ip == nil || !ip.IsLoopback()) {
			logger.Warn("la app no tiene login y usa tu clave de IA: no la expongas fuera de tu máquina", "addr", addr)
		}
	}

	lang := env("CV_LANG", "es")
	if lang != "es" && lang != "en" {
		logger.Warn("CV_LANG no reconocido, uso es", "valor", lang)
		lang = "es"
	}

	st, err := store.New(env("DATA_DIR", "data"))
	if err != nil {
		return err
	}

	apiKey := os.Getenv("ANTHROPIC_API_KEY")
	if apiKey == "" {
		logger.Warn("falta ANTHROPIC_API_KEY: las funciones de IA van a fallar con un mensaje claro")
	}
	analyzer := ai.NewAnthropic(apiKey, os.Getenv("ANTHROPIC_MODEL"))
	if base := os.Getenv("ANTHROPIC_BASE_URL"); base != "" {
		analyzer.BaseURL = base
	}

	var pdf render.PDFRenderer
	var pdfErr error
	if chrome, err := render.NewChromePDF(os.Getenv("CHROME_PATH")); err != nil {
		pdfErr = err
		logger.Warn("no se podrá generar el PDF", "err", err)
	} else {
		pdf = chrome
		logger.Info("Chrome detectado", "path", chrome.Path)
	}

	srv, err := web.New(st, analyzer, pdf, pdfErr, web.Options{Lang: lang, AIConfigured: apiKey != "", Logger: logger})
	if err != nil {
		return err
	}

	httpSrv := &http.Server{
		Addr:              addr,
		Handler:           srv.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       2 * time.Minute,
		WriteTimeout:      6 * time.Minute, // las llamadas a la IA pueden tardar
		IdleTimeout:       2 * time.Minute,
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = httpSrv.Shutdown(shutdownCtx)
	}()

	logger.Info("escuchando", "url", "http://"+addr, "datos", env("DATA_DIR", "data"))
	if err := httpSrv.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}
