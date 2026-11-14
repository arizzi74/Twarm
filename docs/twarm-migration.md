# Migrating MACT to Twarm

Twarm is the new product name. It manages Codex and Claude Code through dedicated
workers, with a shared Web UI and Telegram access. The official repository and
project site move to [arizzi74/Twarm](https://github.com/arizzi74/Twarm) and
[arizzi74.github.io/Twarm](https://arizzi74.github.io/Twarm/).

## Commands and existing installations

`twarm` is the terminal management command. `mact` and `codex-telegramgw` remain
compatibility aliases to the same native manager. Telegram commands retain their
`/tg…` names. For example:

```sh
twarm version
sudo twarm update gateway
twarm update worker
twarm update claude-worker
```

Only run the update command for the component installed under that account.
If an unrelated executable already occupies an alias, the installer preserves it
and reports the collision. Use `codex-telegramgw` to manage this installation.

Configuration, database paths, worker identities, native binaries, service names,
release asset names and `/tgw/` URLs remain unchanged. Browser cookies, passkeys,
trusted devices, PWA app identity and local session preferences keep their existing
identifiers. Existing sessions and project directories are not renamed or moved.
The new logo appears after an app refresh; installed home-screen icons may require
removing and adding the shortcut again because operating systems cache them.

## Signed update bridge

The v0.7.32 manager is published and authenticated under MACT before the repository
is renamed. It resolves the official repository using immutable GitHub repository
ID `1369450599`, accepting only the MACT and Twarm names for that ID. Forks keep
their independent repository selection.

New releases use Twarm's signing identity. Historical releases retain their
original MACT or Codex-Telegram-Gateway identity. Each download is checked against
one explicitly permitted identity and the exact release tag, workflow and source;
a failed signature never causes a fallback to a different repository.

The renamed Twarm repository refuses installing a version older than v0.7.32.
Reading or verifying historical releases remains possible. Saved repository
settings change only after a successful update and readiness check.

A minimal MACT compatibility repository hosts an unchanged copy of the signed
v0.7.32 bridge. The older Codex-Telegram-Gateway compatibility repository hosts
the same release files with a separately verified provenance bundle for its
original signing identity. Compatibility repositories do not receive ongoing
feature releases. Their installer and website point to Twarm.

## Recovering a client that missed the bridge

Update existing installations before switching repository names whenever possible.
The oldest Codex-Telegram-Gateway updater can install its dedicated bridge.
Some intermediate managers saved the Codex repository name but hard-coded the
MACT canonical name; those cannot follow the second rename themselves. Use the
current pinned bootstrap to install the bridge or a newer release:

```sh
# Gateway (Linux)
curl -fsSL https://raw.githubusercontent.com/arizzi74/Twarm/main/scripts/install.sh | sudo sh -s -- update gateway
# Codex worker (run as its owner)
curl -fsSL https://raw.githubusercontent.com/arizzi74/Twarm/main/scripts/install.sh | sh -s -- update worker
# Claude Code worker (run as its owner)
curl -fsSL https://raw.githubusercontent.com/arizzi74/Twarm/main/scripts/install.sh | sh -s -- update claude-worker
```

The current bootstrap pins authenticated bridge-manager hashes independently of
the selected target release. Historical v0.7.32 installer copies still pin the
v0.7.10 manager; the MACT compatibility repository retains those four standalone
files so those historical installer assets remain downloadable. Prefer the
current script above to recover a client.

The public sites retain explicit links from `/MACT/` to `/Twarm/`; GitHub Pages
does not automatically redirect project sites after a repository rename.
