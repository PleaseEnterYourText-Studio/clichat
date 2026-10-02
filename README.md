# clichat

### Chat in your terminal — using the email account you already have

**English** | [中文](README_ZH.md)

[![Release](https://img.shields.io/github/v/release/PleaseEnterYourText-Studio/clichat?include_prereleases&sort=semver&label=release&color=blue)](https://github.com/PleaseEnterYourText-Studio/clichat/releases)
[![Platform](https://img.shields.io/badge/platform-Windows%20%7C%20macOS%20%7C%20Linux-lightgrey)](https://github.com/PleaseEnterYourText-Studio/clichat/releases)
[![Downloads](https://img.shields.io/github/downloads/PleaseEnterYourText-Studio/clichat/total?color=green)](https://github.com/PleaseEnterYourText-Studio/clichat/releases)
[![Go](https://img.shields.io/github/go-mod/go-version/PleaseEnterYourText-Studio/clichat?color=00ADD8&logo=go)](go.mod)

[Download](#download) · [Quick Start](#quick-start) · [Features](#features) · [Shortcuts](#keyboard-shortcuts) · [FAQ](#faq)

---

## Why clichat?

Email is the most universal open messaging system there is. Everyone already has
an address. Nobody has to install anything to talk to you.

But email clients insist on treating it as **correspondence** — subject line,
salutation, signature, quoted history, "Dear Sir/Madam". A conversation that
took four messages over two hours ends up looking like a small pile of paperwork.

clichat just looks at it differently: **treat mail as messages.**

- **Threads, not messages.** Replies are grouped by the `References` /
  `In-Reply-To` headers that mail has always carried. No subject-line guessing,
  because merging two unrelated discussions that happen to share a title is
  worse than leaving them apart.
- **One binary, no server.** IMAP and SMTP go straight from your machine to your
  provider. There is no clichat account, no relay, no third party in the middle.
- **Your credentials stay yours.** The auth code is encrypted with a master
  password you choose, and stored locally. It is never sent anywhere except your
  own mail server.
- **No custom mail format.** v1 does not invent anything on the wire — so
  everything you send stays readable in any other client, and mail sent from
  elsewhere threads correctly in clichat.

## Screenshots

**Conversation view** — group threads are coloured per sender, your own messages
sit on the right:

[![Conversation view](docs/images/01-chat.png)](docs/images/01-chat.png)

**Conversation list** — unread count in the header, `●` for unread, `★` for
starred:

[![Conversation list](docs/images/02-list.png)](docs/images/02-list.png)

**Search** — live filtering across participants, subject and display name:

[![Search](docs/images/03-search.png)](docs/images/03-search.png)

**Help** — press `?` for the full keymap:

[![Help](docs/images/04-help.png)](docs/images/04-help.png)

## Download

Grab a prebuilt binary from the [Releases](https://github.com/PleaseEnterYourText-Studio/clichat/releases)
page. Every commit to `main` publishes a new beta build.

| Platform | File |
|---|---|
| Windows (x64) | `clichat-windows-amd64.exe` |
| Windows (ARM64) | `clichat-windows-arm64.exe` |
| macOS (Apple silicon) | `clichat-darwin-arm64` |
| macOS (Intel) | `clichat-darwin-amd64` |
| Linux (x64) | `clichat-linux-amd64` |
| Linux (ARM64) | `clichat-linux-arm64` |

**macOS** — the binaries are not code-signed, so Gatekeeper will block them on
first run. Clear the quarantine flag:

```bash
chmod +x ./clichat-darwin-arm64
xattr -d com.apple.quarantine ./clichat-darwin-arm64
./clichat-darwin-arm64
```

**Linux** — make it executable first:

```bash
chmod +x ./clichat-linux-amd64
./clichat-linux-amd64
```

**Windows** — SmartScreen may warn about an unsigned binary; click *More info* →
*Run anyway*.

Each release ships a `SHA256SUMS.txt`. Verify with:

```bash
sha256sum -c SHA256SUMS.txt
```

## Quick Start

```bash
clichat
```

On first run a four-step wizard asks for:

1. **Provider** — QQ Mail, 163, 126, Gmail, or custom (fill in the servers yourself)
2. **Email address**
3. **Auth code** — *not* your login password. Nearly every provider requires an
   app-specific password or authorization code for IMAP/SMTP. The wizard shows
   you where to get one for the provider you picked.
4. **Master password** — at least 4 characters. It encrypts the auth code before
   it touches the disk, and is never transmitted anywhere.

You will be asked for the master password every time you start. Forgetting it
means re-configuring the account — press `Ctrl+R` on the unlock screen to do that.

## Features

**Reading**

- Threads grouped by `References` / `In-Reply-To`, not by subject line
- Group conversations detected automatically; each sender gets a stable colour,
  so the same person is the same colour in every session
- HTML mail downgraded to plain text; quoted history trimmed — this is a chat
  view, and quoting is noise in it
- Two-pane layout, collapsing to a single pane under 80 columns

**Acting**

- Compose, reply, reply-all (`Tab` toggles) and forward
- Star (`*`), mark unread (`u`), delete (`d`)
- Delete moves the mail to the server's *Deleted* folder — it is recoverable,
  and it asks for confirmation first
- Copy the last message body to the system clipboard (`y`)
- Search across participants, subject and display name, filtering as you type
- Switch folders (`Tab` in the list) — the folder list comes from the server
  via IMAP `LIST`, so it works whatever your provider calls things

**Keeping it working**

- Cold start pulls only the last 90 days / 500 messages, whichever comes first
- Incremental sync by UID; reconnects resume from the last UID
- `UIDVALIDITY` changes are detected and the local index is rebuilt
- A single unparseable message is skipped and logged, not fatal
- Runs on any provider whose IMAP advertises the `ID` extension (see FAQ)

**Under the hood**

- Pure Go, `CGO_ENABLED=0`, one static binary per platform
- Credentials: Argon2id + NaCl secretbox
- Local index is plain JSON — no database
- Everything on disk lives in one directory you can delete

## Keyboard Shortcuts

Press `?` anywhere for the built-in help page. The list footer always shows the
most common keys.

**Conversation list**

| Key | Action |
|---|---|
| `↑` / `k`, `↓` / `j` | Move |
| `g` / `Home`, `G` / `End` | First / last |
| `[` / `]` | Previous / next unread (wraps around) |
| `Enter` | Open conversation |
| `R` | Open, replying to sender only |
| `n` | New conversation |
| `/` | Search |
| `Tab` | Switch folder |
| `r` | Sync now |
| `u` | Mark unread |
| `*` | Star / unstar |
| `d` | Delete (moves to *Deleted*) |
| `f` | Forward last message |
| `y` | Copy last message body |
| `?` | Help |
| `q` / `Ctrl+C` | Quit |

**Inside a conversation**

The text input has focus here, so single letters would be typed into your
message — every action uses a `Ctrl` combination instead.

| Key | Action |
|---|---|
| `Enter` | Send |
| `Tab` | Reply-all / reply-to-sender |
| `PgUp` / `PgDn` | Scroll |
| `Esc` | Back to list |
| `Ctrl+Y` | Copy last message body |
| `Ctrl+U` | Mark unread and go back |
| `Ctrl+T` | Star / unstar |
| `Ctrl+D` | Delete |
| `F1` | Help |
| `Ctrl+C` | Quit |

## FAQ

**Which providers work out of the box?**

QQ Mail, 163, 126 and Gmail ship as presets — they fill in the servers and ports
and tell you where to get the auth code. Anything else works via *Custom*.

**Why is personal Outlook / Outlook.com not in the list?**

Microsoft disabled IMAP/SMTP basic authentication for personal accounts on
2024-09-16. A password will not connect; it requires OAuth2, which v1 does not
implement. This is a deliberate omission, not an oversight. iCloud is left out
too — its server addresses were never verified, and a wrong preset is worse than
no preset.

**163 / 126 fails with `Unsafe Login. Please contact kefu@188.com`**

NetEase requires clients to announce themselves with an IMAP `ID` command
(RFC 2971) after login, or it rejects the next command. Note the failure is
misleading: `LOGIN` succeeds and the error appears on the following `EXAMINE`.
clichat sends the `ID` command since v0.1.0-beta.9. If you are on an older build,
upgrade.

**Do I have to type the master password every time?**

Yes, in v1. Storing it in the OS keychain is on the roadmap.

**Where is my data stored?**

| Platform | Directory |
|---|---|
| Windows | `%AppData%\clichat` |
| macOS | `~/Library/Application Support/clichat` |
| Linux | `~/.config/clichat` |

| File | Contents | Encrypted |
|---|---|---|
| `config.json` | account, servers, sync policy | no |
| `credentials.enc` | auth code | **yes**, master password |
| `index.json` | message header index | no |
| `clichat.log` | runtime log — events only, no message bodies | no |

Deleting the whole directory resets everything.

**Search does not find an old message.**

Search covers only what is synced locally — by default the last 90 days or 500
messages. It does not query the server. Widen `sync.initial_days` /
`sync.initial_max_messages` in `config.json` if you need more.

**Does `d` really delete the mail?**

No. It moves the mail to the server's *Deleted* folder, and you can recover it
from webmail. If the server supports the IMAP `MOVE` extension clichat uses it;
otherwise it falls back to `COPY` + `\Deleted` + `EXPUNGE`.

**Are attachments supported?**

Not in v1. Attachments are ignored when parsing, and the body shown is the text
part.

**Multiple accounts?**

Not in v1.

**Something is broken — how do I get details?**

The log is at `clichat.log` in the directory above. For connection-layer
problems, set `CLICHAT_IMAP_DEBUG=1` before starting and the full IMAP
conversation is written to the same file — **including message headers and
bodies**, so turn it off when you are done.

## Development

Requires Go 1.26.2 or newer.

```bash
git clone git@github.com:PleaseEnterYourText-Studio/clichat.git
cd clichat

go vet ./...
go test -race ./...
go build -o clichat .
```

The layout, top-down by dependency:

| Package | Responsibility |
|---|---|
| `internal/thread` | Pure threading algorithm (union-find). No IO, fully unit-tested |
| `internal/mail` | IMAP fetch / SMTP send / MIME parsing, behind a `Client` interface |
| `internal/store` | Header index, persisted as JSON |
| `internal/config` | Config, provider presets, Argon2id + secretbox credential encryption |
| `internal/logging` | Runtime log |
| `internal/app` | Orchestration — wires the four above together |
| `internal/tui` | Bubble Tea interface |

Automated tests never touch a real mailbox; `mail.Fake` provides an in-memory
implementation. `internal/tui/screenshot_test.go` regenerates the images used in
this README — see the comment at the top of that file.

## Contributing

Issues and pull requests are welcome. Before opening a PR:

- `go vet ./...` and `go test -race ./...` should pass
- Keep the existing comment style: explain *why*, especially for anything that
  looks like it could be simplified but cannot be

Note: pushing to `main` publishes a release automatically, so work in a branch.

## License

Not specified yet — until then, all rights reserved. If you intend others to use
this, adding a `LICENSE` file is the way to change that.
