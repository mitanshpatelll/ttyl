# dmz

Your DMs in the terminal, without the feed. No stories, no channels, no reels autoplaying,
no "seen" unless you want it.

v0.1 supports **WhatsApp** (linked as a companion device, like WhatsApp Web).

## Build

Needs Go 1.26+ and Xcode Command Line Tools (SQLite uses cgo).

```sh
go build -o bin/dmz ./cmd/dmz
./bin/dmz
```

First run shows a QR code. On your phone: WhatsApp → Settings → Linked devices → Link a device.
Chats fill in as history syncs (can take a few minutes the first time).

## Keys

| Where   | Key                   | Action                       |
|---------|-----------------------|------------------------------|
| Inbox   | `j` / `k`             | Move                         |
|         | `enter` / `l`         | Open chat                    |
|         | `i`                   | Open chat and start typing   |
|         | `/`                   | Filter chats by name         |
|         | `g` / `G`             | Top / bottom                 |
|         | `tab`                 | Jump to open chat            |
| Chat    | `j` / `k`, `d` / `u`  | Scroll, half page            |
|         | `g` / `G`             | Oldest / newest              |
|         | `i` / `enter`         | Reply                        |
|         | `esc` / `h`           | Back to inbox                |
| Compose | `enter` / `esc`       | Send / stop typing           |
| Anywhere| `q` (outside inputs), `ctrl+c` | Quit                |

## Config

`~/Library/Application Support/dmz/config.toml` (created on first run):

```toml
[focus]
read_receipts = false          # true = send "seen" when you open a chat
windows = ["12:30", "19:00"]   # hold messages until these times; [] = live
vip = ["+15551234567"]         # these people bypass delivery windows
```

With delivery windows on, messages that arrive between windows stay hidden until the next
window. The header shows when the next drop is and how many are waiting.

## Data and security

Everything lives in `~/Library/Application Support/dmz/` (override with `DMZ_HOME`), owner-only:

- `whatsapp.db`: session keys. **Anyone with this file is logged in as you.** Never commit or sync it.
- `dmz.db`: your messages. Text, names and media handles are encrypted (XChaCha20-Poly1305) with a
  key kept in the macOS Keychain (service `dmz`). Phone numbers and timestamps are not encrypted.
- `dmz.log`: logs (`DMZ_DEBUG=1` for verbose).

To log out, remove "dmz" under Linked devices on your phone, or delete `whatsapp.db`.

dmz uses WhatsApp's unofficial multi-device protocol via [whatsmeow](https://github.com/tulir/whatsmeow).
Personal low-volume use is the norm for such clients, but it is not sanctioned by WhatsApp.

## Known limits (v0.1)

- Text only out. Media shows as placeholders (`[photo]`, `[voice 0:12]`), nothing downloads.
- Reactions, edits and deletions are not shown.
- Reading in dmz does not clear unread on your phone (that would require sending receipts).
- Your phone must come online at least every ~14 days or WhatsApp unlinks companion devices.
- Group names for groups joined mid-session appear after the first message or a restart.

## Layout

```
cmd/dmz            entrypoint: config, login, wiring
internal/core      platform-neutral types + Platform interface
internal/platform  adapters (whatsapp)
internal/store     SQLite: chats, messages, read markers, delivery-window queries
internal/rules     delivery windows
internal/app       events -> store, focus rules, UI data
internal/tui       Bubble Tea UI
```

## Roadmap

- Daemon/TUI split (sessions stay online when the UI is closed)
- Encrypted message store
- Telegram, then Instagram
- Media open via `o`, weekly stats, daily time budget
