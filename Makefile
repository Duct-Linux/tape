MODULES = common cli daemon builder repo

# Target platform. Override on the command line for cross builds:
#   make build GOARCH=arm64
#   make build GOARCH=arm GOARM=7 TAPE_ARCH=armv7h
#   make build GOARCH=arm GOARM=6 TAPE_ARCH=armv6h
GOOS   ?= linux
GOARCH ?= amd64
GOARM  ?=

# The architecture name baked into the binaries. GOARCH is "arm" for both armv6
# and armv7 and Go does not expose GOARM at runtime, so a 32-bit ARM build must
# say which one it is or it will accept packages its hardware cannot run.
# Empty means "derive it from GOARCH", which is correct for every other target.
TAPE_ARCH ?=

# CGO is off: the sqlite driver is pure Go, so every target cross-compiles with
# no toolchain beyond Go itself. That matters for a package manager that has to
# ship for every architecture its distribution supports.
env = CGO_ENABLED=0 GOOS=$(GOOS) GOARCH=$(GOARCH) $(if $(GOARM),GOARM=$(GOARM),)

# -w and -s drop DWARF and the symbol table. -X bakes in the architecture name.
ldflags = -ldflags '-w -s $(if $(TAPE_ARCH),-X tape/common/arch.buildArch=$(TAPE_ARCH),)'

# Where built binaries land. build-all overrides it per architecture: six cross
# builds writing to one directory leave only the last one, which looked like a
# successful multi-arch build and produced a single riscv64 set.
BIN ?= ./bin

# Installation layout. DESTDIR supports staged installs (packaging, chroots).
DESTDIR ?=
PREFIX  ?= /usr
BINDIR  ?= $(PREFIX)/bin
SYSCONFDIR ?= /etc

.PHONY: build build-daemon build-cli build-builder build-repo \
        install clean go-tidy test vet lint check fmt build-all

build: build-daemon build-cli build-builder build-repo

$(BIN):
	mkdir -p $(BIN)

# -s -w already strips these binaries; a separate strip(1) pass added nothing
# and is not available for the target arch when cross-compiling.
build-daemon: $(BIN)
	$(env) go build $(ldflags) -o $(BIN)/taped ./daemon/main.go

build-cli: $(BIN)
	$(env) go build $(ldflags) -o $(BIN)/tape ./cli/main.go

build-builder: $(BIN)
	$(env) go build $(ldflags) -o $(BIN)/tape-builder ./builder/main.go

build-repo: $(BIN)
	$(env) go build $(ldflags) -o $(BIN)/tape-repo ./repo/main.go

install: build
	install -d $(DESTDIR)$(BINDIR)
	install -m 0755 $(BIN)/taped        $(DESTDIR)$(BINDIR)/taped
	install -m 0755 $(BIN)/tape         $(DESTDIR)$(BINDIR)/tape
	install -m 0755 $(BIN)/tape-builder $(DESTDIR)$(BINDIR)/tape-builder
	install -m 0755 $(BIN)/tape-repo    $(DESTDIR)$(BINDIR)/tape-repo
	install -d $(DESTDIR)$(SYSCONFDIR)/tape/repos
	install -d $(DESTDIR)$(SYSCONFDIR)/tape/keys
	install -d $(DESTDIR)/var/cache/tape/repos
	install -m 0644 ./common/config/sample_config.toml $(DESTDIR)$(SYSCONFDIR)/tape/config.toml.sample

test:
	@for m in $(MODULES); do \
		echo "== test $$m =="; \
		go test -race ./$$m/... || exit 1; \
	done

vet:
	@for m in $(MODULES); do \
		echo "== vet $$m =="; \
		go vet ./$$m/... || exit 1; \
	done

fmt:
	gofmt -w $(MODULES)

# gofmt must report no files; staticcheck is optional so a missing binary is
# not a hard failure.
lint:
	@test -z "$$(gofmt -l $(MODULES))" || { echo "gofmt needed:"; gofmt -l $(MODULES); exit 1; }
	@command -v staticcheck >/dev/null 2>&1 && \
		for m in $(MODULES); do staticcheck ./$$m/... || exit 1; done || \
		echo "staticcheck not installed, skipping"

check: lint vet test

clean:
	rm -rf ./bin

# Build for every architecture the distribution targets, each into its own
# directory under ./bin. They all wrote to ./bin itself before, so five of the
# six were overwritten and the run finished with one riscv64 set of binaries
# named as though they were the lot.
build-all:
	$(MAKE) build GOARCH=amd64            TAPE_ARCH=x86_64  BIN=./bin/x86_64
	$(MAKE) build GOARCH=arm64            TAPE_ARCH=aarch64 BIN=./bin/aarch64
	$(MAKE) build GOARCH=arm   GOARM=7    TAPE_ARCH=armv7h  BIN=./bin/armv7h
	$(MAKE) build GOARCH=arm   GOARM=6    TAPE_ARCH=armv6h  BIN=./bin/armv6h
	$(MAKE) build GOARCH=386              TAPE_ARCH=i686    BIN=./bin/i686
	$(MAKE) build GOARCH=riscv64          TAPE_ARCH=riscv64 BIN=./bin/riscv64

go-tidy:
	@for m in $(MODULES); do \
		(cd ./$$m && go mod tidy) || exit 1; \
	done
