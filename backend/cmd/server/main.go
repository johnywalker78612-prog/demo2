package main

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"private-records/internal/app"
	"strings"
	"syscall"
	"time"
)

func secret() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return base64.StdEncoding.EncodeToString(b)
}
func main() {
	init := flag.Bool("init", false, "print freshly generated local environment settings")
	migrate := flag.Bool("migrate", false, "apply the cloud store schema using DATABASE_URL_UNPOOLED")
	importFile := flag.String("import-file", "", "copy an encrypted local store into an empty cloud store and exit")
	flag.Parse()
	if *init {
		fmt.Printf("RECORDS_ADMIN_PASSWORD='%s'\nRECORDS_API_TOKEN='%s'\nRECORDS_DATA_KEY='%s'\n", secret(), secret(), secret())
		return
	}
	if *migrate {
		url := os.Getenv("DATABASE_URL_UNPOOLED")
		if url == "" {
			log.Fatal("Set DATABASE_URL_UNPOOLED for migrations")
		}
		if err := app.MigratePostgres(url); err != nil {
			log.Fatal(err)
		}
		log.Print("Cloud store schema is ready")
		return
	}
	key, err := base64.StdEncoding.DecodeString(os.Getenv("RECORDS_DATA_KEY"))
	if err != nil || len(key) != 32 {
		log.Fatal("Set RECORDS_DATA_KEY to a base64-encoded 32-byte key; use -init to generate configuration")
	}
	path := os.Getenv("RECORDS_DATA_FILE")
	if path == "" {
		path = "data/records.enc"
	}
	var store *app.Store
	if url := os.Getenv("DATABASE_URL"); url != "" {
		store, err = app.OpenPostgresStore(url, key)
	} else {
		if os.Getenv("RECORDS_CLOUD") == "true" {
			log.Fatal("Cloud mode requires DATABASE_URL; local storage is not persistent on Render Free")
		}
		store, err = app.OpenStore(path, key)
	}
	if err != nil {
		log.Fatal(err)
	}
	defer store.Close()
	if *importFile != "" {
		if _, err := os.Stat(*importFile); err != nil {
			log.Fatal("Import file does not exist or cannot be read")
		}
		if err := store.ImportLocalFile(*importFile, key); err != nil {
			log.Fatal(err)
		}
		log.Print("Encrypted local records imported; source file unchanged")
		return
	}
	publicURL := os.Getenv("RECORDS_PUBLIC_URL")
	if publicURL == "" && os.Getenv("RECORDS_CLOUD") == "true" {
		publicURL = os.Getenv("RENDER_EXTERNAL_URL")
	}
	handler, err := app.New(store, app.Config{EmailLogoURL: os.Getenv("RECORDS_EMAIL_LOGO_URL"), PublicURL: publicURL, SMTP: app.SMTPConfig{Host: os.Getenv("RECORDS_SMTP_HOST"), Port: os.Getenv("RECORDS_SMTP_PORT"), Username: os.Getenv("RECORDS_SMTP_USERNAME"), Password: os.Getenv("RECORDS_SMTP_PASSWORD"), From: os.Getenv("RECORDS_SMTP_FROM"), BrevoAPIKey: os.Getenv("BREVO_API_KEY"), FromEmail: os.Getenv("FROM_EMAIL"), FromName: os.Getenv("FROM_NAME"), ImplicitTLS: os.Getenv("RECORDS_SMTP_IMPLICIT_TLS") == "true"}, AdminUsername: os.Getenv("RECORDS_ADMIN_USERNAME"), AdminEmail: os.Getenv("RECORDS_ADMIN_EMAIL"), AdminPassword: os.Getenv("RECORDS_ADMIN_PASSWORD"), APIToken: os.Getenv("RECORDS_API_TOKEN"), SecureCookies: os.Getenv("RECORDS_SECURE_COOKIES") == "true"})
	if err != nil {
		log.Fatal(err)
	}
	addr := os.Getenv("RECORDS_ADDR")
	if addr == "" {
		addr = "localhost:8081"
	}
	if os.Getenv("RECORDS_CLOUD") == "true" {
		port := os.Getenv("PORT")
		if port == "" {
			port = "10000"
		}
		addr = net.JoinHostPort("0.0.0.0", port)
		if os.Getenv("RECORDS_SECURE_COOKIES") != "true" || !strings.HasPrefix(publicURL, "https://") {
			log.Fatal("Cloud mode requires RECORDS_SECURE_COOKIES=true and an HTTPS RECORDS_PUBLIC_URL")
		}
	} else {
		host, _, err := net.SplitHostPort(addr)
		if err != nil || !strings.EqualFold(host, "localhost") {
			log.Fatal("Local mode must bind to localhost:<port>; use RECORDS_CLOUD=true for hosting")
		}
	}
	// net/http diagnostics can include peer addresses; do not retain them.
	server := &http.Server{Addr: addr, Handler: handler, ErrorLog: log.New(io.Discard, "", 0), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second, WriteTimeout: 30 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 16 << 10}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdown)
	}()
	emailMode := "development outbox (no email sent)"
	if os.Getenv("BREVO_API_KEY") != "" {
		emailMode = "Brevo API configured"
	} else if os.Getenv("RECORDS_SMTP_HOST") != "" {
		emailMode = "SMTP configured"
	}
	log.Printf("Private Records listening on %s; admin portal at /admin; %s", addr, emailMode)
	if err = server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatal(err)
	}
}
