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
			logger.Warn("las sesiones guardan API keys en memoria y el CV es compartido: no expongas la app fuera de tu máquina", "addr", addr)
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

	// BYOK: cada persona entra con su propia API key desde /login. Las URLs
	// base se pueden cambiar para usar un proxy o un servidor de pruebas.
	registry := ai.NewRegistry()
	for p, envName := range map[ai.Provider]string{
		ai.ProviderAnthropic: "ANTHROPIC_BASE_URL",
		ai.ProviderOpenAI:    "OPENAI_BASE_URL",
		ai.ProviderGemini:    "GEMINI_BASE_URL",
		ai.ProviderGroq:      "GROQ_BASE_URL",
	} {
		if base := os.Getenv(envName); base != "" {
			registry.BaseURLs[p] = base
		}
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

	srv, err := web.New(st, registry, pdf, pdfErr, web.Options{Lang: lang, Logger: logger})
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
