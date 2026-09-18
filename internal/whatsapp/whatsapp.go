// Package whatsapp wraps the whatsmeow client lifecycle: pairing and
// inbound-event handling.
package whatsapp

import (
	"context"
	"fmt"
	"os"

	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/types/events"
	waLog "go.mau.fi/whatsmeow/util/log"

	"github.com/mdp/qrterminal/v3"
	qrcode "github.com/skip2/go-qrcode"

	"github.com/sivashankar/whatsapp_agent/internal/messages"
	"github.com/sivashankar/whatsapp_agent/internal/minio"
)

// Pair renders login QR codes until the phone scans one. whatsmeow rotates
// the code roughly every 20s and pushes each one down the channel, so this
// reprints as needed and returns when the channel closes.
func Pair(ctx context.Context, client *whatsmeow.Client, logger waLog.Logger, qrs *minio.Store) error {
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
				} else if err := qrs.Put(ctx, png); err != nil {
					logger.Warnf("upload qr: %v", err)
				} else {
					fmt.Printf("...or open %s/%s (refreshes about every 20s)\n",
						qrs.Bucket, minio.Object)
				}
			}
		case "success":
			logger.Infof("paired successfully")
			if qrs != nil {
				qrs.Remove(ctx)
			}
		case "timeout":
			logger.Warnf("QR expired before it was scanned — restart to get a new one")
			if qrs != nil {
				qrs.Remove(ctx)
			}
		default:
			logger.Infof("login event: %s", evt.Event)
		}
	}
	return nil
}

// HandleEvent is the whatsmeow event handler: it logs inbound messages and
// records them to msgStore, and logs connection lifecycle events.
func HandleEvent(ctx context.Context, logger waLog.Logger, msgStore *messages.Store, evt any) {
	switch v := evt.(type) {
	case *events.Message:
		// messages.ExtractText is the single place that knows how to pull a
		// body out of the ~50-way message union, so the log line matches
		// exactly what gets stored.
		logger.Infof("message chat=%s sender=%s fromMe=%v text=%q",
			v.Info.Chat, v.Info.Sender, v.Info.IsFromMe, messages.ExtractText(v.Message))
		msgStore.Record(ctx, v)
	case *events.Connected:
		logger.Infof("connected to WhatsApp")
	case *events.LoggedOut:
		logger.Warnf("logged out (%s) — clear the whatsmeow_device row and restart to pair again", v.Reason)
	}
}
