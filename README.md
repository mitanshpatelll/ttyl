# dmz

Your DMs in the terminal, without the feed.

WhatsApp, Telegram and Instagram chats in one keyboard-driven TUI. No stories, no reels,
no channels, no "seen" unless you turn it on. Media is a placeholder until you ask for it.
Messages can arrive in batches at times you choose, and a daily budget locks dmz when
your time is up.

dmz does not reduce screen time (a terminal is a screen). It removes the parts of messaging
apps built to keep you scrolling, so answering a message stays answering a message.

| Platform  | How dmz connects                                    | Status        |
|-----------|-----------------------------------------------------|---------------|
| WhatsApp  | Linked device, QR code (whatsmeow)                  | stable        |
| Telegram  | Official API with your own api_id (gotd)            | works, young  |
| Instagram | Browser session cookies, private protocol (instameow) | experimental |

## Install

Needs Go 1.26+ and Xcode Command Line Tools (SQLite uses cgo). Built and tested on macOS;
Linux is untested (it would need a Secret Service keyring for the keys).

```sh
make build      # bin/dmz
make install    # into $GOBIN
make test
```

## First run

```sh
dmz                    # nothing linked yet: starts WhatsApp linking
dmz login telegram
dmz login instagram
```

**WhatsApp:** scan the QR code from WhatsApp → Settings → Linked devices → Link a device.
Your chats fill in as history syncs (a few minutes the first time). Your phone must come
online at least every ~14 days or WhatsApp unlinks companion devices.

**Telegram:** create a free API app at <https://my.telegram.org> → API development tools, put
`api_id` and `api_hash` under `[telegram]` in the config, then `dmz login telegram` asks for
your phone number, the code Telegram sends, and your two-step password if you have one.
dmz shows up as "dmz" under Telegram → Settings → Devices.

**Instagram (experimental):** Meta does not allow unofficial clients; an account can be
challenged, rate limited or suspended. Try a secondary account first. `dmz login instagram`
asks for cookies from a browser: log in to instagram.com in a private window, open the
developer tools → Application (Safari: Storage) → Cookies, copy `sessionid`, `csrftoken`
and `ds_user_id` (optionally `mid`, `ig_did`, `rur`), then close the window without logging
out.

## Using it

Press `?` for every key. The essentials:

| Key | Action |
|---|---|
| `j` / `k` | move in the inbox, select a message in a chat |
| `enter` | open chat / send |
| `i` | reply |
| `o` | open the selected message's attachment |
| `/` | filter chats by name |
| `esc` | back / stop typing |
| `q`, `ctrl+c` | quit |

Moving past the oldest message in a chat loads older ones.

```sh
dmz stats          # last 7 days: time, opens, sent, received, extensions
dmz stats 30
dmz logout telegram [--purge]   # --purge also deletes its stored messages
dmz version
```

## Focus features

- **No read receipts** by default. Reading in dmz only moves a local marker.
- **No feeds.** WhatsApp status updates and channels, Telegram broadcast channels, Instagram
  stories: never stored, never shown.
- **Media as text:** `[photo]`, `[voice 0:12]`, `[file report.pdf]`. Shared reels show as
  `[reel]` plus the link, never a preview. `o` downloads on demand; images, video, audio, PDF
  and text open in their default app, anything else (scripts, apps, archives) is only revealed
  in Finder, and every file gets macOS's quarantine flag.
- **Delivery windows:** messages that arrive between windows stay hidden until the next one.
  VIPs come through immediately.
- **Daily budget:** only time with dmz focused counts. When it runs out, dmz locks; type
  `extend` for five more minutes. Extensions show up in `dmz stats`.
- No typing indicators, no notifications.

## Config

`~/Library/Application Support/dmz/config.toml`, created on first run:

```toml
[focus]
read_receipts = false          # true = send "seen" when you open a chat
windows = ["12:30", "19:00"]   # hold messages until these times; [] = live
vip = ["+15551234567"]         # WhatsApp numbers that bypass delivery windows
daily_minutes = 30             # 0 = no budget

[telegram]
api_id = 1234567
api_hash = "0123456789abcdef0123456789abcdef"
```

## Data and security

Everything lives in `~/Library/Application Support/dmz/` (or `$DMZ_HOME`), directory `0700`,
files `0600`:

| File | What |
|---|---|
| `whatsapp.db` | WhatsApp session keys. **Anyone with this file is logged in as you.** |
| `dmz.db` | your messages; text, names and media handles encrypted (XChaCha20-Poly1305) |
| `telegram.json` | Telegram peers and catch-up marks (no secrets) |
| `dmz.log` | logs; `DMZ_DEBUG=1` for verbose |

The message store key, the Telegram session and the Instagram cookies are in the macOS
Keychain (service `dmz`). Phone numbers, user IDs and timestamps in `dmz.db` are not
encrypted, and `whatsapp.db` is whatsmeow's own unencrypted store. Opened attachments are
cached in `~/Library/Caches/dmz/media` for 7 days.

Never commit or sync the data folder.

## Known limits

- Text only when sending; no reactions, replies or media from dmz.
- WhatsApp: reading in dmz doesn't clear unread on the phone unless `read_receipts = true`.
- Telegram: reactions aren't shown (Telegram sends counts, not people). Missed messages
  are caught up from the 100 most recent chats on connect; edits and deletes made while dmz
  was closed are not.
- Instagram: only the inbox snapshot plus live messages (no deep history), no end-to-end
  encrypted chats, and Meta changes the protocol often.
- VIPs are WhatsApp numbers only.

## How it fits together

```
cmd/dmz              commands, setup, account wiring
internal/core        platform-neutral chats, messages, events, Platform interface
internal/platform/*  whatsapp (whatsmeow), telegram (gotd), instagram (instameow)
internal/app         events → store, focus rules, media opening, usage
internal/store       SQLite: migrations, encryption, visibility queries
internal/rules       delivery windows
internal/secret      Keychain
internal/tui         Bubble Tea UI
```

Each adapter turns its protocol into `core` events; the app writes them to one store and
the UI reads from it. Delivery windows and VIPs are applied when querying, so nothing is
ever rewritten to hide or release messages.

Built on [whatsmeow](https://github.com/tulir/whatsmeow), [gotd](https://github.com/gotd/td),
[mautrix-meta](https://github.com/mautrix/meta) and [Bubble Tea](https://github.com/charmbracelet/bubbletea).
