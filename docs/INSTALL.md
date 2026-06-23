# Installing ESFS

ESFS ships five binaries: `esfs` (CLI), `esfsd` (mount daemon), and the routed
shims `esfs-grep`, `esfs-ls`, `esfs-find`. It needs a **FUSE backend** at runtime
(it builds without one).

After installing, verify everything with:

```sh
esfs doctor
```

## 1. Install a FUSE backend

### Linux

```sh
# Debian/Ubuntu
sudo apt-get install -y fuse3

# Fedora/RHEL
sudo dnf install -y fuse3

# Arch
sudo pacman -S fuse3
```

Confirm `/dev/fuse` exists. Non-root mounts work by default; if you need
`allow_other`, enable `user_allow_other` in `/etc/fuse.conf`.

### macOS

Pick one (kextless preferred):

- **FUSE-T** (kextless, recommended): install from <https://www.fuse-t.org/>.
- **macFUSE** (kernel extension): install from <https://macfuse.io>. On Apple
  Silicon this requires approving the system extension and may require enabling
  reduced security in Recovery — see `esfs doctor` output for guidance.

On macOS, `/esfs` may not be writable as a mount root; ESFS supports
`/Volumes/esfs` with an optional `/esfs` symlink. Any path works via `--mount`.

## 2. Install ESFS

### Option A — Homebrew (macOS)

Releases publish a cask to the `dbmurphy/homebrew-tap` tap (Homebrew casks are
macOS-only; on Linux use Option B or C):

```sh
brew install --cask dbmurphy/tap/esfs
# or:
brew tap dbmurphy/tap && brew install --cask esfs
```

### Option B — Go toolchain (Linux + macOS)

```sh
go install github.com/dbmurphy/elasticsearch-filesystem/cmd/esfs@latest
go install github.com/dbmurphy/elasticsearch-filesystem/cmd/esfsd@latest
go install github.com/dbmurphy/elasticsearch-filesystem/cmd/esfs-grep@latest
go install github.com/dbmurphy/elasticsearch-filesystem/cmd/esfs-ls@latest
go install github.com/dbmurphy/elasticsearch-filesystem/cmd/esfs-find@latest
```

### Option C — prebuilt binaries

Download the tarball for your platform from the
[Releases page](https://github.com/dbmurphy/elasticsearch-filesystem/releases)
and place the binaries on your `PATH`:

```sh
tar xzf esfs_<version>_<os>_<arch>.tar.gz
sudo install -m 0755 esfs esfsd esfs-grep esfs-ls esfs-find /usr/local/bin/
```

### Option D — build from source

Requires Go 1.26+:

```sh
git clone https://github.com/dbmurphy/elasticsearch-filesystem && cd elasticsearch-filesystem
make build           # -> ./bin/{esfs,esfsd,esfs-grep,esfs-ls,esfs-find}
make install         # -> /usr/local/bin (override with PREFIX=...)
```

Cross-compiled release tarballs for linux/darwin amd64/arm64:

```sh
make cross           # -> dist/
```

## 3. Configure

Set an endpoint and credentials (see [CONFIGURATION.md](CONFIGURATION.md)):

```sh
export ESFS_ENDPOINT=https://localhost:9200
export ES_API_KEY=...            # referenced via api_key_env
# or copy and edit a config file:
mkdir -p ~/.config/esfs && cp config.example.json ~/.config/esfs/config.json
```

## 4. First run

```sh
esfs doctor                      # verify FUSE, ES, auth, routing, shims
esfs mount --mount /mnt/esfs     # mount the whole cluster (Ctrl-C to unmount)
eval "$(esfs env --mount /mnt/esfs)"   # activate routed ls/find/grep
```

## 5. Run as a service (optional)

- **Linux (systemd user unit):** copy `packaging/esfs.service` to
  `~/.config/systemd/user/esfs.service`, then
  `systemctl --user enable --now esfs`.
- **macOS (launchd):** copy `packaging/com.esfs.daemon.plist` to
  `~/Library/LaunchAgents/`, then
  `launchctl load ~/Library/LaunchAgents/com.esfs.daemon.plist`.

## 6. Shell integration / Bash Tool mode

Routed `ls`/`find`/`grep` activate only when ESFS routing is enabled:

```sh
eval "$(esfs env)"               # prepends shim dir to PATH + sets ESFS_MOUNT
```

For agent Bash Tool runtimes, set the same `ESFS_MOUNT` and put the shim
directory (printed by `esfs env`) ahead of system tools on `PATH`.

## Uninstall

```sh
make uninstall                   # or: rm /usr/local/bin/{esfs,esfsd,esfs-grep,esfs-ls,esfs-find}
rm -rf ~/.config/esfs ~/.cache/esfs
```

## Troubleshooting

Run `esfs doctor` first — it reports the failing layer (FUSE backend, ES
connectivity, auth, index visibility, routing activation, shim install, sync
policy). Common issues:

- **mount fails**: no FUSE backend — see step 1.
- **`grep` not routing**: routing not activated — run `eval "$(esfs env)"`.
- **`--since` errors**: configure `time_field` for the index (see CONFIGURATION.md).
- **cannot reach Elasticsearch**: check `ESFS_ENDPOINT`, TLS (`ca_cert_file`/`insecure`), and the API key.
