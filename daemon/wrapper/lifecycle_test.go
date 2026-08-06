package wrapper

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"encoding/gob"
	"os"
	"path/filepath"
	"strings"
	"tape/common/arch"
	"tape/common/enums"
	"tape/common/global"
	"tape/common/structs"
	"tape/daemon/utils"
	"testing"

	"github.com/spf13/viper"
)

func init() {
	gob.Register(map[string]string{})
	gob.Register([]map[string]string{})
}

// collector captures the response stream a wrapper writes, standing in for a
// connected client.
type collector struct {
	buf bytes.Buffer
	enc *gob.Encoder
}

func newCollector() *collector {
	c := &collector{}
	c.enc = gob.NewEncoder(&c.buf)
	return c
}

func (c *collector) responses(t *testing.T) []structs.Response {
	t.Helper()

	dec := gob.NewDecoder(bytes.NewReader(c.buf.Bytes()))
	var out []structs.Response
	for {
		var resp structs.Response
		if err := dec.Decode(&resp); err != nil {
			return out
		}
		out = append(out, resp)
	}
}

func (c *collector) terminal(t *testing.T) *structs.Response {
	t.Helper()

	for _, r := range c.responses(t) {
		if r.Type == enums.ResponseTypeDone || r.Type == enums.ResponseTypeError {
			return &r
		}
	}
	t.Fatal("no terminal response was written")
	return nil
}

// setupDaemonConfig points the daemon at a throwaway sysroot and database.
func setupDaemonConfig(t *testing.T) (sysroot string) {
	t.Helper()

	root := t.TempDir()
	sysroot = filepath.Join(root, "sysroot")
	if err := os.MkdirAll(sysroot, 0755); err != nil {
		t.Fatal(err)
	}

	cfg := viper.New()
	cfg.Set("daemon.sysroot", sysroot)
	cfg.Set("daemon.installed-db", filepath.Join(root, "installed.db"))
	global.SetConfig(cfg)

	return sysroot
}

// stageArchive writes a package into the daemon's download cache, which is the
// only place LocalInstall will accept a path from.
func stageArchive(t *testing.T, name, version string, deps map[string]string, files map[string]string) string {
	t.Helper()

	cacheDir, err := os.MkdirTemp(utils.PkgCacheRoot(), "pkg-")
	if err != nil {
		if mkErr := os.MkdirAll(utils.PkgCacheRoot(), 0755); mkErr != nil {
			t.Fatal(mkErr)
		}
		cacheDir, err = os.MkdirTemp(utils.PkgCacheRoot(), "pkg-")
		if err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() { os.RemoveAll(cacheDir) })

	var buf bytes.Buffer
	gzw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gzw)

	var depLines strings.Builder
	for depName, constraint := range deps {
		depLines.WriteString(depName + " = '" + constraint + "'\n")
	}
	manifest := "[dependencies]\n" + depLines.String() +
		"\n[package]\narch = '" + arch.Current() + "'\nname = '" + name + "'\n" +
		"subversion = '1'\nversion = '" + version + "'\n"

	write := func(path, body string, mode int64) {
		hdr := &tar.Header{Name: path, Mode: mode, Size: int64(len(body)), Typeflag: tar.TypeReg}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}

	write("TAPEPACKAGE.toml", manifest, 0644)
	for path, body := range files {
		write("install/"+path, body, 0644)
	}

	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gzw.Close(); err != nil {
		t.Fatal(err)
	}

	archive := filepath.Join(cacheDir, name+"-"+version+"-1."+arch.Current()+".tape.tar.gz")
	if err := os.WriteFile(archive, buf.Bytes(), 0644); err != nil {
		t.Fatal(err)
	}
	return archive
}

// The whole point of phase 3: a package can be installed, listed and removed
// through the daemon's request handlers.
func TestInstallListRemoveLifecycle(t *testing.T) {
	sysroot := setupDaemonConfig(t)

	archive := stageArchive(t, "demo", "1.0", map[string]string{"libdemo": "1.0"}, map[string]string{
		"usr/bin/demo":     "#!/bin/sh\n",
		"usr/share/doc/rm": "docs",
	})

	// --- install ---
	c := newCollector()
	if err := LocalInstall(&structs.Request{
		Type: enums.RequestTypeLocalInstall,
		Data: archive,
	}, c.enc); err != nil {
		t.Fatalf("LocalInstall: %v", err)
	}
	if resp := c.terminal(t); resp.Type != enums.ResponseTypeDone {
		t.Fatalf("install terminal response = %v (%v), want Done", resp.Type, resp.Data)
	}

	if _, err := os.Stat(filepath.Join(sysroot, "usr/bin/demo")); err != nil {
		t.Fatalf("file was not installed into the sysroot: %v", err)
	}

	// --- list ---
	c = newCollector()
	if err := ListPkgs(&structs.Request{Type: enums.RequestTypeListPkgs}, c.enc); err != nil {
		t.Fatalf("ListPkgs: %v", err)
	}
	listed, ok := c.terminal(t).Data.([]map[string]string)
	if !ok {
		t.Fatalf("list returned %T, want []map[string]string", c.terminal(t).Data)
	}
	if len(listed) != 1 || listed[0]["name"] != "demo" {
		t.Fatalf("list = %v, want one entry for demo", listed)
	}
	if listed[0]["reason"] != "explicit" {
		t.Errorf("reason = %q, want explicit", listed[0]["reason"])
	}
	if listed[0]["version"] != "1.0" {
		t.Errorf("version = %q, want 1.0", listed[0]["version"])
	}

	// --- remove ---
	c = newCollector()
	if err := RemovePkg(&structs.Request{
		Type: enums.RequestTypeRemovePkg,
		Data: "demo",
	}, c.enc); err != nil {
		t.Fatalf("RemovePkg: %v", err)
	}
	summary, ok := c.terminal(t).Data.(map[string]string)
	if !ok {
		t.Fatalf("remove returned %T, want map[string]string", c.terminal(t).Data)
	}
	if summary["files"] != "2" {
		t.Errorf("files removed = %q, want 2", summary["files"])
	}

	if _, err := os.Stat(filepath.Join(sysroot, "usr/bin/demo")); err == nil {
		t.Error("file survived removal")
	}

	// --- list again ---
	c = newCollector()
	if err := ListPkgs(&structs.Request{Type: enums.RequestTypeListPkgs}, c.enc); err != nil {
		t.Fatal(err)
	}
	listed, _ = c.terminal(t).Data.([]map[string]string)
	if len(listed) != 0 {
		t.Errorf("list after removal = %v, want empty", listed)
	}
}

// A dependency-installed package shows up as an orphan once its dependent goes.
func TestOrphanReportingThroughTheWire(t *testing.T) {
	setupDaemonConfig(t)

	lib := stageArchive(t, "libdemo", "1.0", nil, map[string]string{"usr/lib/libdemo.so": "ELF"})
	app := stageArchive(t, "demo", "1.0", map[string]string{"libdemo": "1.0"}, map[string]string{"usr/bin/demo": "x"})

	c := newCollector()
	if err := LocalInstall(&structs.Request{
		Type:    enums.RequestTypeLocalInstall,
		Data:    lib,
		Options: map[string]interface{}{"asDependency": true},
	}, c.enc); err != nil {
		t.Fatal(err)
	}

	c = newCollector()
	if err := LocalInstall(&structs.Request{Type: enums.RequestTypeLocalInstall, Data: app}, c.enc); err != nil {
		t.Fatal(err)
	}

	// libdemo is still needed, so removing it must be refused.
	c = newCollector()
	err := RemovePkg(&structs.Request{Type: enums.RequestTypeRemovePkg, Data: "libdemo"}, c.enc)
	if err == nil {
		t.Fatal("removing a still-required package was allowed")
	}

	// Remove the dependent; libdemo becomes an orphan.
	c = newCollector()
	if err := RemovePkg(&structs.Request{Type: enums.RequestTypeRemovePkg, Data: "demo"}, c.enc); err != nil {
		t.Fatal(err)
	}
	summary, _ := c.terminal(t).Data.(map[string]string)
	if summary["orphans"] != "libdemo" {
		t.Errorf("orphans = %q, want libdemo", summary["orphans"])
	}

	// And the orphans filter finds it.
	c = newCollector()
	if err := ListPkgs(&structs.Request{
		Type:    enums.RequestTypeListPkgs,
		Options: map[string]interface{}{"orphansOnly": true},
	}, c.enc); err != nil {
		t.Fatal(err)
	}
	listed, _ := c.terminal(t).Data.([]map[string]string)
	if len(listed) != 1 || listed[0]["name"] != "libdemo" {
		t.Errorf("orphans list = %v, want [libdemo]", listed)
	}
}

func TestRemoveUnknownPackageReportsError(t *testing.T) {
	setupDaemonConfig(t)

	c := newCollector()
	err := RemovePkg(&structs.Request{Type: enums.RequestTypeRemovePkg, Data: "not-installed"}, c.enc)
	if err == nil {
		t.Fatal("removing a package that is not installed succeeded, want error")
	}
	if !strings.Contains(err.Error(), "not installed") {
		t.Errorf("error %q should say the package is not installed", err)
	}
}
