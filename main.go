// Command chishenme runs the household meal plan.
package main

import (
	"context"
	"crypto/rand"
	"crypto/tls"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"chishenme/internal/server"
	"chishenme/internal/store"
)

func main() {
	log.SetFlags(log.LstdFlags)
	if err := run(); err != nil {
		log.Fatalf("chishenme: %v", err)
	}
}

func run() error {
	addr := env("CHISHENME_ADDR", ":18082")
	dbPath := env("CHISHENME_DB", "chishenme.db")
	tlsConfig, err := tlsSetup()
	if err != nil {
		return err
	}
	secret, err := sessionSecret()
	if err != nil {
		return err
	}
	st, err := store.Open(dbPath, store.Admin{
		Username: env("CHISHENME_ADMIN_USER", "admin"),
		Password: os.Getenv("CHISHENME_ADMIN_PASSWORD"),
	})
	if err != nil {
		return err
	}
	defer st.Close()
	if name := st.CreatedAdmin(); name != "" {
		log.Printf("chishenme: created the first account %q", name)
	} else if os.Getenv("CHISHENME_ADMIN_PASSWORD") != "" {
		log.Print("chishenme: an account already exists, so CHISHENME_ADMIN_USER and CHISHENME_ADMIN_PASSWORD are ignored")
	}

	handler, err := server.New(st, server.Config{
		Secret:      secret,
		Secure:      tlsConfig != nil,
		DeepSeekKey: os.Getenv("CHISHENME_DEEPSEEK_KEY"),
		DeepSeekURL: os.Getenv("CHISHENME_DEEPSEEK_URL"),
	})
	if err != nil {
		return err
	}
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("listen on CHISHENME_ADDR=%q: %w", addr, err)
	}
	scheme := "http"
	if tlsConfig != nil {
		listener = tls.NewListener(listener, tlsConfig)
		scheme = "https"
	}
	srv := &http.Server{
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
		WriteTimeout:      90 * time.Second,
		IdleTimeout:       2 * time.Minute,
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	errc := make(chan error, 1)
	go func() { errc <- srv.Serve(listener) }()
	log.Printf("chishenme: listening on %s://%s", scheme, listener.Addr())

	select {
	case err := <-errc:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
		shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return srv.Shutdown(shutdown)
	}
}

func tlsSetup() (*tls.Config, error) {
	certFile, keyFile := os.Getenv("CHISHENME_TLS_CERT"), os.Getenv("CHISHENME_TLS_KEY")
	if certFile == "" && keyFile == "" {
		return nil, nil
	}
	if certFile == "" || keyFile == "" {
		return nil, errors.New("CHISHENME_TLS_CERT and CHISHENME_TLS_KEY must be set together")
	}
	certificate, err := tls.LoadX509KeyPair(certFile, keyFile)
	if err != nil {
		return nil, fmt.Errorf("load TLS certificate: %w", err)
	}
	return &tls.Config{MinVersion: tls.VersionTLS12, Certificates: []tls.Certificate{certificate}}, nil
}

func sessionSecret() ([]byte, error) {
	if secret := os.Getenv("CHISHENME_SECRET"); secret != "" {
		if len(secret) < 16 {
			return nil, errors.New("CHISHENME_SECRET must be at least 16 characters")
		}
		return []byte(secret), nil
	}
	secret := make([]byte, 32)
	if _, err := rand.Read(secret); err != nil {
		return nil, fmt.Errorf("generate session secret: %w", err)
	}
	log.Print("chishenme: CHISHENME_SECRET is unset, using a random key; logins reset on restart")
	return secret, nil
}

func env(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}
