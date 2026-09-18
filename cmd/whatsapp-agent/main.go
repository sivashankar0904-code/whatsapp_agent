// Command whatsapp-agent is a connection spike for the WhatsApp multidevice API.
//
// It pairs as a linked device via QR, persists the session to Postgres, and
// logs inbound messages. It deliberately sends nothing: this phase only proves
// the pipe works. Reply logic and chat allowlisting come later.
//
// Configuration is entirely by environment variable:
//
//	DB_HOST, DB_PORT, DB_NAME, DB_USER, DB_PASSWORD, DB_SSL_MODE
//	                  Postgres connection; all but DB_PORT and DB_SSL_MODE
//	                  are required (those default to 5432 and "disable")
//	WA_S3_ENDPOINT    optional, host:port for publishing the pairing QR
//	WA_S3_BUCKET      optional, defaults to "whatsapp-agent"
//	WA_S3_ACCESS_KEY  S3 credentials
//	WA_S3_SECRET_KEY
//	WA_S3_USE_SSL     optional, "true" to use HTTPS
//	WA_API_ADDR       optional, listen address for the message API, defaults
//	                  to ":8081". Unauthenticated — see server.New.
//
// The process keeps no state on disk, so it runs unprivileged in a container
// with a read-only filesystem. Running more than one instance against the same
// session corrupts Signal state, so scale is fixed at one replica.
package main

import (
	"context"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/store/sqlstore"
	waLog "go.mau.fi/whatsmeow/util/log"

	// Postgres driver for whatsmeow's own session store, which manages its
	// own *sql.DB internally. pgx is pure Go, so the build needs no C
	// compiler. The app's own tables use pgxpool instead — see datastore.
	_ "github.com/jackc/pgx/v5/stdlib"

	"github.com/sivashankar/whatsapp_agent/internal/chats"
	"github.com/sivashankar/whatsapp_agent/internal/config"
	"github.com/sivashankar/whatsapp_agent/internal/datastore"
	"github.com/sivashankar/whatsapp_agent/internal/messages"
	"github.com/sivashankar/whatsapp_agent/internal/minio"
	"github.com/sivashankar/whatsapp_agent/internal/server"
	"github.com/sivashankar/whatsapp_agent/internal/whatsapp"
)

func main() {
	ctx := context.Background()
	logger := waLog.Stdout("Main", "DEBUG", true)

	// SIGTERM is what `docker stop` sends, so handling it is what makes
	// shutdown graceful rather than a 10s kill.
	sigC := make(chan os.Signal, 1)
	signal.Notify(sigC, os.Interrupt, syscall.SIGTERM)

	cfg, err := config.Load()
	if err != nil {
		logger.Errorf("%v", err)
		os.Exit(1)
	}
	logger.Infof("session store: postgres")
	container, err := sqlstore.New(ctx, "pgx", cfg.DB.DSN(), waLog.Stdout("Database", "WARN", true))
	if err != nil {
		logger.Errorf("open session store: %v", err)
		os.Exit(1)
	}
	defer container.Close()

	// A second connection pool to the same database for the app's own table
	// — see the Connect doc comment for why this is kept separate from
	// whatsmeow's own connection.
	pool, err := datastore.Connect(ctx, cfg.DB)
	if err != nil {
		logger.Errorf("open message store: %v", err)
		os.Exit(1)
	}
	defer pool.Close()
	if err := messages.EnsureTable(ctx, pool); err != nil {
		logger.Errorf("create messages table: %v", err)
		os.Exit(1)
	}
	msgStore := messages.NewStore(pool, logger)

	if err := chats.EnsureTable(ctx, pool); err != nil {
		logger.Errorf("create chats table: %v", err)
		os.Exit(1)
	}
	chatStore := chats.NewStore(pool)

	// A nil-ID device means nothing has been paired yet; whatsmeow fills it in
	// once a QR scan succeeds.
	deviceStore, err := container.GetFirstDevice(ctx)
	if err != nil {
		logger.Errorf("load device: %v", err)
		os.Exit(1)
	}

	client := whatsmeow.NewClient(deviceStore, waLog.Stdout("Client", "INFO", true))
	client.AddEventHandler(func(evt any) { whatsapp.HandleEvent(ctx, logger, msgStore, chatStore, evt) })

	httpSrv := &http.Server{Addr: cfg.APIAddr, Handler: server.New(msgStore, chatStore)}
	go func() {
		if err := httpSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Errorf("http server: %v", err)
		}
	}()
	logger.Infof("message API listening on %s", cfg.APIAddr)
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := httpSrv.Shutdown(shutdownCtx); err != nil {
			logger.Warnf("http shutdown: %v", err)
		}
	}()

	if client.Store.ID == nil {
		// Only needed while pairing; an already-paired run never publishes a QR.
		qrs, err := minio.New(ctx, cfg.S3, logger)
		if err != nil {
			logger.Errorf("qr storage: %v", err)
			os.Exit(1)
		}
		if qrs == nil {
			logger.Warnf("WA_S3_ENDPOINT not set — QR will only appear in the logs")
		}
		if err := whatsapp.Pair(ctx, client, logger, qrs); err != nil {
			logger.Errorf("pair: %v", err)
			os.Exit(1)
		}
	} else {
		logger.Infof("existing session for %s, reconnecting without QR", client.Store.ID)
		if err := client.Connect(); err != nil {
			logger.Errorf("connect: %v", err)
			os.Exit(1)
		}
	}

	logger.Infof("running — Ctrl-C to stop")
	<-sigC

	logger.Infof("shutting down")
	client.Disconnect()
}
