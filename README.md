# tape

The package manager for the **Duct** Linux distribution.

> ⚠️ **Disclaimer**
>
> tape is still in its very first development stage. Therefore it does contain bugs and will break your system. **DO NOT** use it in production, unless you know what you are doing.
>
> ~ ItzYanick

## What is it?

tape is the package manager for Duct. It is written in Go and is designed to be fast and easy to use.

## What is it made of?

tape is made of several components:

| module | binary | what it does |
|---|---|---|
| `cli` | `tape` | the command line interface |
| `daemon` | `taped` | the package manager daemon |
| `builder` | `tape-builder` | builds a package from a `TAPEBUILD.toml` |
| `repo` | `tape-repo` | creates, indexes and signs repositories |
| `common` | -- | shared code between all the components |

The `daemon` is responsible for managing the packages and the repositories. It is also responsible for downloading and installing packages. The `cli` is the interface to the `daemon`. It is used to install, remove, update and search for packages.

`tape-builder` is used to build packages. It is not required to use tape, but it is recommended to use it.
`tape-repo` is used to create a repo. You can add, remove, update packages with it.

## Where it puts things

| what | where |
|---|---|
| configuration | `/etc/tape/config.toml` |
| repository definitions | `/etc/tape/repos/*.toml` |
| trusted public keys | `/etc/tape/keys/*.pub` |
| installed-package database | `/var/lib/tape/installed.db` |
| downloaded indexes | `/var/cache/tape/repos` |
| socket, pid, log | `/var/run/tape.sock`, `/var/run/tape.pid`, `/var/log/tape.log` |

`TAPE_CONFIG_DIR` and `TAPE_CACHE_DIR` override the first two groups, which is
what makes it possible to run the daemon against a chroot or a throwaway tree
without touching the running system.

Package archives are named `<name>-<version>-<subversion>.<arch>.tape.tar.gz`
and carry a `TAPEPACKAGE.toml` manifest; build recipes are `TAPEBUILD.toml`.

## How do I use it?

You cannot install tape currently. You can build it yourself if you want to test
it, or use the reproducible builder image in `../docker`.

## Architectures

tape is architecture-aware end to end: packages record what they were built for,
and the daemon installs only what matches.

| tape name | GOARCH  | also accepted as              |
|-----------|---------|-------------------------------|
| `x86_64`  | amd64   | `amd64`, `x86-64`             |
| `i686`    | 386     | `i386`, `x86`                 |
| `aarch64` | arm64   | `arm64`, `armv8`, `armv8l`    |
| `armv7h`  | arm     | `armv7`, `armv7l`, `armhf`    |
| `armv6h`  | arm     | `armv6`, `armv6l`             |
| `riscv64` | riscv64 | `rv64`                        |
| `any`     | --      | `noarch`, `all`               |

A package marked `any` installs anywhere. Anything else must match exactly:
armv7 binaries use instructions armv6 hardware does not have, so those two are
not interchangeable in either direction.

### Building

Everything is pure Go -- the sqlite driver included -- so cross-compiling needs
no toolchain beyond Go itself:

```sh
make build GOARCH=arm64                  TAPE_ARCH=aarch64
make build GOARCH=arm GOARM=7            TAPE_ARCH=armv7h
make build GOARCH=arm GOARM=6            TAPE_ARCH=armv6h
make build-all                           # every architecture above
```

`TAPE_ARCH` is baked into the binary at link time. It matters on 32-bit ARM:
`GOARCH` is just `arm` for both armv6 and armv7, and Go does not expose `GOARM`
at runtime, so without it an armv6 build would report itself as `armv7h` and
accept packages its hardware cannot execute. Set the `TAPE_ARCH` environment
variable at runtime to override the baked-in value.

### Building packages for another architecture

```sh
tape-builder build ./mypkg -t aarch64-linux-gnu -o ./out
```

`--target` sets the architecture stamped into the package, so a cross-built
package is labelled for the machine it is for rather than the one that built it.
It is also exported to build scripts as `$TAPE_TARGET`.

## Package signing

tape verifies repositories before it trusts anything they say. The chain is:

1. A repository publishes `repo.db` and a detached signature `repo.db.sig`.
2. The client verifies that signature against a public key in `/etc/tape/keys`.
3. The verified index carries a `sha256` for every package, so each downloaded
   archive is checked against a digest that the signature already covers.

One signature therefore protects the whole repository; individual packages are
not signed separately.

### Publishing a signed repository

```sh
# Once: create a signing key. Keep the private half off client machines.
tape-repo generate-key /secure/place/myrepo.key

# Build the repository as usual.
tape-repo create-repo ./myrepo
tape-repo add-to-repo ./myrepo ./some-package.tape.tar.gz

# Sign it. Re-run this after every add-to-repo: changing the index
# invalidates the signature, and add-to-repo deletes the stale one.
tape-repo sign-repo ./myrepo /secure/place/myrepo.key --name myrepo
```

`--name` must match the name clients know the repository by (the filename of
its `.toml` in `/etc/tape/repos`). The signature is bound to it, so a signature
made for one repository cannot be replayed against another.

### Trusting a key on a client

Copy the public half into the keyring:

```sh
cp myrepo.key.pub /etc/tape/keys/<keyid>.pub
```

`generate-key` prints the key id. The id is derived from the key material, so a
key file cannot claim an identity that does not match its contents.

### Unsigned repositories

Verification is on by default; a repository with no signature is refused. To
accept one anyway, set it explicitly in that repository's config:

```toml
allow-unsigned = true
```

This disables verification for the index *and* every package it serves. It is
intended for local build trees, not for anything fetched over a network.

### Limitations

- The private key is stored unencrypted; its file permissions (0600, enforced
  on load) are the only thing protecting it.
- There is no key revocation, and no rollback protection: a signed but stale
  index still verifies. The signature records a `created` timestamp that a
  future freshness check can use.
