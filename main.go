// Command whatsapp_agent is a connection spike for the WhatsApp multidevice API.
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
//	                  to ":8081". Unauthenticated — see startAPI.
//
// The process keeps no state on disk, so it runs unprivileged in a container
// with a read-only filesystem. Running more than one instance against the same
// session corrupts Signal state, so scale is fixed at one replica.
package main

import (
	"bytes"
	"context"
	"database/sql"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/store/sqlstore"
	"go.mau.fi/whatsmeow/types/events"
	waLog "go.mau.fi/whatsmeow/util/log"

	"github.com/joho/godotenv"
	"github.com/mdp/qrterminal/v3"
	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
	qrcode "github.com/skip2/go-qrcode"

	// Postgres driver for the session store. pgx is pure Go, so the build
	// needs no C compiler.
	_ "github.com/jackc/pgx/v5/stdlib"
)

// storeConfig returns the sqlstore dialect and address, built from discrete
// DB_* variables rather than one URL, matching how the rest of this
// deployment's services are configured. There is no local-file fallback: a
// missing required variable is a startup error, not a silent switch to a
// different store. Credentials belong in the environment, never in this file.
func storeConfig() (dialect, address string, err error) {
	// A .env file in the working directory is a convenience for local runs;
	// real environment variables always win, and a missing file is fine.
	_ = godotenv.Load()

	required := map[string]string{
		"DB_HOST":     os.Getenv("DB_HOST"),
		"DB_NAME":     os.Getenv("DB_NAME"),
		"DB_USER":     os.Getenv("DB_USER"),
		"DB_PASSWORD": os.Getenv("DB_PASSWORD"),
	}
	for name, val := range required {
		if val == "" {
			return "", "", fmt.Errorf("%s is not set — export it or put it in a .env file", name)
		}
	}

	port := os.Getenv("DB_PORT")
	if port == "" {
		port = "5432"
	}
	sslMode := os.Getenv("DB_SSL_MODE")
	if sslMode == "" {
		sslMode = "disable"
	}

	// url.URL escapes the user and password, so a password containing "@" or
	// "/" cannot be misparsed as part of the host or path.
	dsn := url.URL{
		Scheme:   "postgres",
		User:     url.UserPassword(required["DB_USER"], required["DB_PASSWORD"]),
		Host:     net.JoinHostPort(required["DB_HOST"], port),
		Path:     "/" + required["DB_NAME"],
		RawQuery: "sslmode=" + sslMode,
	}
	// dbutil.ParseDialect maps any "postgres"-prefixed name to Postgres, and
	// pgx registers its database/sql driver under "pgx".
	return "pgx", dsn.String(), nil
}

// qrObject is where the pairing QR is published while login is pending. The
// container has no display and no useful filesystem, so the PNG goes to object
// storage where it can be opened from a browser, and is deleted as soon as
// pairing succeeds — a live pairing code is a credential.
const qrObject = "qr.png"

// qrStore publishes the pairing QR to S3-compatible object storage. All fields
// come from the environment; it is disabled when WA_S3_ENDPOINT is unset, in
// which case the terminal rendering is the only output.
type qrStore struct {
	client *minio.Client
	bucket string
	logger waLog.Logger
}

// newQRStore returns nil (not an error) when object storage is not configured,
// so pairing still works with terminal output alone.
func newQRStore(ctx context.Context, logger waLog.Logger) (*qrStore, error) {
	endpoint := os.Getenv("WA_S3_ENDPOINT")
	if endpoint == "" {
		return nil, nil
	}
	bucket := os.Getenv("WA_S3_BUCKET")
	if bucket == "" {
		bucket = "whatsapp-agent"
	}
	useSSL := strings.EqualFold(os.Getenv("WA_S3_USE_SSL"), "true")

	client, err := minio.New(endpoint, &minio.Options{
		Creds: credentials.NewStaticV4(
			os.Getenv("WA_S3_ACCESS_KEY"),
			os.Getenv("WA_S3_SECRET_KEY"),
			"",
		),
		Secure: useSSL,
	})
	if err != nil {
		return nil, fmt.Errorf("s3 client: %w", err)
	}

	exists, err := client.BucketExists(ctx, bucket)
	if err != nil {
		return nil, fmt.Errorf("check bucket %q: %w", bucket, err)
	}
	if !exists {
		if err := client.MakeBucket(ctx, bucket, minio.MakeBucketOptions{}); err != nil {
			return nil, fmt.Errorf("create bucket %q: %w", bucket, err)
		}
		logger.Infof("created bucket %s", bucket)
	}
	return &qrStore{client: client, bucket: bucket, logger: logger}, nil
}

// put uploads the QR PNG, replacing any previous one.
func (q *qrStore) put(ctx context.Context, png []byte) error {
	_, err := q.client.PutObject(ctx, q.bucket, qrObject,
		bytes.NewReader(png), int64(len(png)),
		minio.PutObjectOptions{ContentType: "image/png"})
	return err
}

// remove deletes the published QR. Called once pairing succeeds or the code
// expires, so a scannable credential is not left in the bucket.
func (q *qrStore) remove(ctx context.Context) {
	if err := q.client.RemoveObject(ctx, q.bucket, qrObject,
		minio.RemoveObjectOptions{}); err != nil {
		q.logger.Warnf("remove %s from bucket: %v", qrObject, err)
	}
}

func main() {
	ctx := context.Background()
	logger := waLog.Stdout("Main", "DEBUG", true)

	// SIGTERM is what `docker stop` sends, so handling it is what makes
	// shutdown graceful rather than a 10s kill.
	sigC := make(chan os.Signal, 1)
	signal.Notify(sigC, os.Interrupt, syscall.SIGTERM)

	dialect, address, err := storeConfig()
	if err != nil {
		logger.Errorf("%v", err)
		os.Exit(1)
	}
	logger.Infof("session store: postgres")
	container, err := sqlstore.New(ctx, dialect, address, waLog.Stdout("Database", "WARN", true))
	if err != nil {
		logger.Errorf("open session store: %v", err)
		os.Exit(1)
	}
	defer container.Close()

	// A second connection pool to the same database for the app's own table.
	// sqlstore.Container does not expose the *sql.DB it wraps, and mixing this
	// table into whatsmeow's own connection would tie its lifecycle to code
	// whatsmeow does not know about, so this is intentionally separate.
	msgDB, err := sql.Open(dialect, address)
	if err != nil {
		logger.Errorf("open message store: %v", err)
		os.Exit(1)
	}
	defer msgDB.Close()
	if err := ensureMessagesTable(ctx, msgDB); err != nil {
		logger.Errorf("create messages table: %v", err)
		os.Exit(1)
	}
	msgStore := &messageStore{db: msgDB, logger: logger}

	// A nil-ID device means nothing has been paired yet; whatsmeow fills it in
	// once a QR scan succeeds.
	deviceStore, err := container.GetFirstDevice(ctx)
	if err != nil {
		logger.Errorf("load device: %v", err)
		os.Exit(1)
	}

	client := whatsmeow.NewClient(deviceStore, waLog.Stdout("Client", "INFO", true))
	client.AddEventHandler(func(evt any) { handleEvent(ctx, logger, msgStore, evt) })

	httpSrv := startAPI(logger, msgStore)
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := httpSrv.Shutdown(shutdownCtx); err != nil {
			logger.Warnf("http shutdown: %v", err)
		}
	}()

	if client.Store.ID == nil {
		// Only needed while pairing; an already-paired run never publishes a QR.
		qrs, err := newQRStore(ctx, logger)
		if err != nil {
			logger.Errorf("qr storage: %v", err)
			os.Exit(1)
		}
		if qrs == nil {
			logger.Warnf("WA_S3_ENDPOINT not set — QR will only appear in the logs")
		}
		if err := pair(ctx, client, logger, qrs); err != nil {
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

// pair renders login QR codes until the phone scans one. whatsmeow rotates the
// code roughly every 20s and pushes each one down the channel, so this reprints
// as needed and returns when the channel closes.
func pair(ctx context.Context, client *whatsmeow.Client, logger waLog.Logger, qrs *qrStore) error {
	qrChan, err := client.GetQRChannel(ctx)
	if err != nil {
		return fmt.Errorf("qr channel: %w", err)
	}
	if err := client.Connect(); err != nil {
		return fmt.Errorf("connect: %w", err)
	}

	for evt := range qrChan {
		switch evt.Event {
		case "code":
			fmt.Println("\nScan with WhatsApp > Settings > Linked devices > Link a device:")
			qrterminal.GenerateHalfBlock(evt.Code, qrterminal.L, os.Stdout)

			// Terminal rendering depends on the font and colour scheme, and in
			// a container it is only visible through the logs, so also publish
			// a PNG. Replaced on every rotation so the object always matches
			// the live code.
			if qrs != nil {
				png, err := qrcode.Encode(evt.Code, qrcode.Medium, 512)
				if err != nil {
					logger.Warnf("encode qr: %v", err)
				} else if err := qrs.put(ctx, png); err != nil {
					logger.Warnf("upload qr: %v", err)
				} else {
					fmt.Printf("...or open %s/%s (refreshes about every 20s)\n",
						qrs.bucket, qrObject)
				}
			}
		case "success":
			logger.Infof("paired successfully")
			if qrs != nil {
				qrs.remove(ctx)
			}
		case "timeout":
			logger.Warnf("QR expired before it was scanned — restart to get a new one")
			if qrs != nil {
				qrs.remove(ctx)
			}
		default:
			logger.Infof("login event: %s", evt.Event)
		}
	}
	return nil
}

func handleEvent(ctx context.Context, logger waLog.Logger, msgStore *messageStore, evt any) {
	switch v := evt.(type) {
	case *events.Message:
		// extractText (messages.go) is the single place that knows how to pull
		// a body out of the ~50-way message union, so the log line matches
		// exactly what gets stored.
		logger.Infof("message chat=%s sender=%s fromMe=%v text=%q",
			v.Info.Chat, v.Info.Sender, v.Info.IsFromMe, extractText(v.Message))
		msgStore.record(ctx, v)
	case *events.Connected:
		logger.Infof("connected to WhatsApp")
	case *events.LoggedOut:
		logger.Warnf("logged out (%s) — clear the whatsmeow_device row and restart to pair again", v.Reason)
	}
}

// startAPI serves the read-only message API in the background and returns
// immediately; the caller shuts it down via the returned server's Shutdown.
//
// It binds to all interfaces because it is meant to be reached from other
// containers on the same docker network, not from the internet — do not
// publish this port on a host interface without adding authentication first,
// since GET /messages currently has none.
func startAPI(logger waLog.Logger, msgStore *messageStore) *http.Server {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /messages", msgStore.handleList)
	mux.HandleFunc("GET /health", handleHealth)

	addr := os.Getenv("WA_API_ADDR")
	if addr == "" {
		addr = ":8081"
	}
	srv := &http.Server{Addr: addr, Handler: mux}
	go func() {
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Errorf("http server: %v", err)
		}
	}()
	logger.Infof("message API listening on %s", addr)
	return srv
}
