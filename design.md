# WhatsApp Agent — Design

A WhatsApp bot that reads and replies to messages, built as a Go service that
speaks WhatsApp's multidevice protocol directly.

**Status:** connection layer complete and verified. Reply logic not yet built.

---

## 1. Why not a browser

The project began as "can Chrome CLI give me the WhatsApp Web QR code?" It can —
drive Chrome over CDP, wait for the QR `<canvas>`, and read the `data-ref`
attribute. But that path was rejected.

WhatsApp Web is not a website login. Scanning its QR **pairs a new device** onto
the account: the browser receives its own identity keys, its own Signal session
with every contact, and messages independently of the phone. The browser is one
implementation of that device, not the device itself.

[`whatsmeow`](https://github.com/tulir/whatsmeow) is another implementation — the
same protocol, natively in Go, over a WebSocket.

| | Browser automation | whatsmeow |
|---|---|---|
| Runs | Chrome + WhatsApp's JS app + driver | one process, one WebSocket |
| Reading a message | scrape minified DOM | typed event with real fields |
| Memory | 300 MB – 1 GB | ~40 MB |
| Breaks when | WhatsApp ships a frontend redesign | the wire protocol changes (rare) |
| Headless server | awkward | native |

The deciding factor is the second row. WhatsApp reships its frontend constantly;
the wire protocol is comparatively stable, and whatsmeow absorbs changes upstream.

### Why Go, not Python

Python was the initial preference. whatsmeow is a Go library with no direct
Python import, leaving three options:

1. **`neonize`** — Python bindings wrapping whatsmeow's Go core via ctypes.
   735★ but `subscribers_count: 6`, effectively single-maintainer, with 70 open
   issues.
2. **A Go sidecar** — write the Go anyway, plus a socket protocol and two
   languages to maintain.
3. **Pure Go** — depend only on whatsmeow directly.

Option 3 won. The binding layer in options 1 and 2 was the fragile part, and the
whole connection spike is ~120 lines of Go. whatsmeow itself is 7.3k★ and
actively maintained (pushed within a day of this work).

The Python preference was for a later LLM phase; that phase was deferred, making
the tradeoff moot for now.

---

## 2. Current architecture

Single Go binary, one file.

```
main.go
  ├── acquireLock()    single-instance guard (PID file, self-healing)
  ├── storeConfig()    .env + WA_DATABASE_URL → pgx dialect
  ├── pair()           QR pairing, first run only
  ├── handleEvent()    inbound event logging (sends nothing)
  └── printContacts()  `contacts` subcommand
```

### Dependencies (all pure Go — no cgo, no C compiler)

| Module | Version | Role |
|---|---|---|
| `go.mau.fi/whatsmeow` | `v0.0.0-20260915211301` | protocol client |
| `github.com/jackc/pgx/v5` | `v5.11.0` | Postgres driver |
| `github.com/joho/godotenv` | `v1.5.1` | `.env` loading |
| `github.com/mdp/qrterminal/v3` | `v3.2.1` | QR in terminal |
| `github.com/skip2/go-qrcode` | `v0.0.0-20200617` | QR as PNG |

Build: `CGO_ENABLED=0 go build` → ~26 MB static binary.

`GOTOOLCHAIN=local` is used because whatsmeow's `go.mod` names toolchain
`1.27.0`; local Go 1.26.4 satisfies the actual `go 1.26.0` requirement, so this
avoids a needless toolchain download.

---

## 3. Session storage

**Postgres is required.** There is no local-file fallback: a missing
`WA_DATABASE_URL` is a startup error, not a silent switch to another store.

```
WA_DATABASE_URL=postgres://postgres:PASSWORD@127.0.0.1:5432/whatsapp_agent?sslmode=disable
```

Loaded from `.env` (gitignored) or the environment; real env vars win.

### Dialect detail

`sqlstore.New(ctx, dialect, address, log)` passes `dialect` to **both**
`sql.Open` (needs a driver name) and `dbutil.ParseDialect` (needs a known
dialect). `ParseDialect` matches `strings.HasPrefix(engine, "postgres")` **or**
`engine == "pgx"`, and pgx registers its driver as `"pgx"` — so `"pgx"`
satisfies both. Verified by reading the upstream source, not assumed.

The same check drove the earlier SQLite choice: `modernc.org/sqlite` registers
`"sqlite"`, which `ParseDialect` also accepts via prefix — that let the project
build without gcc when SQLite was still in use. SQLite has since been removed.

### Schema

whatsmeow creates and migrates 17 `whatsmeow_*` tables in database
`whatsapp_agent`, schema `public`. The load-bearing ones:

| Table | Holds |
|---|---|
| `whatsmeow_device` | **the pairing** — JID and device identity keys |
| `whatsmeow_contacts` | address book |
| `whatsmeow_sessions` | Signal sessions per contact |
| `whatsmeow_identity_keys` / `whatsmeow_pre_keys` / `whatsmeow_sender_keys` | key material |
| `whatsmeow_app_state_*` (3) | sync state — contacts and settings arrive here |
| `whatsmeow_version` | migration version |

**`whatsmeow_device` + `whatsmeow_identity_keys` is the WhatsApp identity.**
Anyone who can read those rows can impersonate the linked device. It currently
shares a Postgres instance with unrelated `iam` and billing databases, so it is
only as protected as that container.

---

## 4. Pairing

The answer to the original QR question, at protocol level:

```go
if client.Store.ID == nil {
    qrChan, _ := client.GetQRChannel(ctx)
    client.Connect()
    for evt := range qrChan {
        if evt.Event == "code" { /* render evt.Code */ }
    }
} else {
    client.Connect()   // already paired — no QR
}
```

`client.Store.ID == nil` **is** the "scan once" requirement. `evt.Code` is the
same payload WhatsApp Web puts in `data-ref` — delivered on a channel rather
than scraped. `GetQRChannel` re-emits on the ~20 s rotation automatically.

The code is rendered **twice**: as terminal half-blocks, and as `qr.png` (512 px,
medium recovery), because terminal rendering depends on font and colour scheme.
Both refresh each rotation; the PNG is deleted on success.

---

## 5. Concurrency constraint

**One connection per linked device.** A second connection gets the first kicked
off with `StreamReplaced`, and two processes sharing the store corrupt each
other's Signal sessions — which makes messages permanently undecryptable.

This was hit in practice: running a second instance against a shared store
produced `SQLITE_BUSY`, `failed to decrypt prekey message`, and failed app-state
syncs.

Mitigation is `wa.lock`, a PID file created with `O_CREATE|O_EXCL` so two racing
processes cannot both win. A lock whose PID no longer exists is reclaimed
automatically, so a crash needs no manual cleanup.

**Platform trap, found by testing:** `processAlive` cannot use `Signal(0)` alone.
On Windows `Signal` is unimplemented and returns `"not supported by windows"`
*even for a live process*, so treating any error as "dead" made every lock look
stale and silently defeated the guard. The check now branches on `runtime.GOOS`:
Windows relies on `FindProcess` (which opens a real handle and fails for dead
PIDs); Unix keeps signal-0, where `EPERM` means alive-but-not-ours.

The lock is local, so it does not constrain a second machine pointed at the same
database. WhatsApp's own limit is the backstop there.

---

## 6. Contacts

`contacts` connects, waits for sync, prints a table, exits.

Contacts are **not** available at connect time. They arrive via the
`critical_unblock_low` app-state patch and are mass-inserted on full sync, so
reading immediately after connecting returns an empty store. The command waits
on `events.AppStateSyncComplete` for that patch, with a 60 s timeout that falls
back to whatever is stored.

Name resolution prefers `FullName` → `FirstName` → `BusinessName` → `PushName`,
and the printed `SOURCE` column shows which was used. Mostly `push` means the
address-book sync has not landed — those are names contacts chose for
themselves.

---

## 7. Verified behaviour

| Check | Result |
|---|---|
| `CGO_ENABLED=0 go build` | passes, no C compiler |
| `go vet`, `gofmt` | clean |
| Schema migration | 17 tables created in Postgres |
| First run | QR rendered and scanned, device paired |
| Restart | `reconnecting without QR` — **scan-once requirement met** |
| Paired device | `919597497765:28@s.whatsapp.net` |
| `contacts` | 957 rows |
| Stale lock (dead PID) | reclaimed |
| Live lock (real PID) | refused |
| Missing `WA_DATABASE_URL` | clear startup error |
| Run from WSL | works against the WSL Docker Postgres |

---

## 8. Safety posture

This phase **sends nothing**. `handleEvent` only logs, so there is no way to
reply to a contact or group while testing.

Known risks:

- **Account ban.** This is an unofficial client — the same exposure as browser
  automation. Use a number you can afford to lose; no bulk or unsolicited
  sending. The sanctioned alternative is the WhatsApp Business Cloud API (no QR,
  no ban risk, but needs a Business account and a separate number).
- **The phone still anchors the account**, as with WhatsApp Web. It need not stay
  online continuously.
- **Credentials on disk.** `.env` holds the Postgres password; `qr.png` is a live
  pairing code. Both are gitignored, which prevents commits but nothing else.
- **Contact data.** `contacts` prints ~957 real phone numbers.

---

## 9. Next phase — replies

Not built. Design intent:

1. **Gates before any send**, in order:
   - `IsFromMe` → drop (otherwise the bot answers itself and can loop)
   - chat JID not in allowlist → drop; **the allowlist starts empty**, since a
     linked device receives the entire message stream and an open default would
     reply to every contact and group on first connect
   - text lacks the `!` prefix → drop
2. **A command table** of pure functions (`ping`, `echo`, `help`), unit-testable
   with no network or database.
3. **`dispatch(text) → string | nil`**, where `nil` means "no matching command".
   That nil return is the seam an LLM handler later fills.

Deferred deliberately: LLM replies, media, group management, message archiving
(whatsmeow stores session and crypto state, not a searchable message history).

### Cleanup outstanding

- Delete the legacy `session.db` (old SQLite pairing credentials) and unlink that
  device from the phone to free its slot.
