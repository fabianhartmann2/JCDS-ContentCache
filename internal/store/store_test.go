package store

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestValidateFilename(t *testing.T) {
	tests := map[string]bool{
		"ExampleFile.pkg":              true,
		"Microsoft Office (16.99).pkg": true,
		"../secret.pkg":                false,
		"folder/file.pkg":              false,
		"https:evil.pkg":               false,
		"example.dmg":                  false,
		".hidden.pkg":                  false,
	}
	for filename, wantValid := range tests {
		t.Run(filename, func(t *testing.T) {
			err := ValidateFilename(filename)
			if wantValid && err != nil {
				t.Fatalf("ValidateFilename() error = %v", err)
			}
			if !wantValid && err == nil {
				t.Fatal("ValidateFilename() unexpectedly accepted filename")
			}
		})
	}
}

func TestPendingPublishesCompleteFileAtomically(t *testing.T) {
	root := t.TempDir()
	storeRoot := filepath.Join(root, "packages")
	tempRoot := filepath.Join(root, ".temporary")
	packageStore, err := New(storeRoot, tempRoot)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	pending, err := packageStore.Begin("ExampleFile.pkg")
	if err != nil {
		t.Fatalf("Begin() error = %v", err)
	}
	defer pending.Abort()

	content := []byte("package content")
	if _, err := pending.Write(content); err != nil {
		t.Fatalf("Write() error = %v", err)
	}
	if _, err := os.Stat(filepath.Join(storeRoot, "ExampleFile.pkg")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("final path existed before Commit(), error = %v", err)
	}
	if err := pending.Commit(int64(len(content))); err != nil {
		t.Fatalf("Commit() error = %v", err)
	}

	file, _, err := packageStore.Open("ExampleFile.pkg")
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer file.Close()
	got, err := io.ReadAll(file)
	if err != nil {
		t.Fatalf("ReadAll() error = %v", err)
	}
	if string(got) != string(content) {
		t.Fatalf("stored content = %q, want %q", got, content)
	}
}

func TestAbortDoesNotPublishFile(t *testing.T) {
	root := t.TempDir()
	packageStore, err := New(filepath.Join(root, "packages"), filepath.Join(root, ".temporary"))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	pending, err := packageStore.Begin("ExampleFile.pkg")
	if err != nil {
		t.Fatalf("Begin() error = %v", err)
	}
	if _, err := pending.Write([]byte("partial")); err != nil {
		t.Fatalf("Write() error = %v", err)
	}
	if err := pending.Abort(); err != nil {
		t.Fatalf("Abort() error = %v", err)
	}
	if _, _, err := packageStore.Open("ExampleFile.pkg"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Open() error = %v, want ErrNotFound", err)
	}
}

func TestReserveCapacityAccountsForHeadroomAndConcurrentFills(t *testing.T) {
	root := t.TempDir()
	packageStore, err := New(filepath.Join(root, "packages"), filepath.Join(root, ".temporary"))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	packageStore.space = func() (int64, int64, error) {
		return 1000, 1000, nil
	}

	releaseFirst, err := packageStore.ReserveCapacity(400, 100, 20)
	if err != nil {
		t.Fatalf("first ReserveCapacity() error = %v", err)
	}
	defer releaseFirst()

	if _, err := packageStore.ReserveCapacity(500, 100, 20); !errors.Is(err, ErrInsufficientSpace) {
		t.Fatalf("second ReserveCapacity() error = %v, want ErrInsufficientSpace", err)
	}

	releaseFirst()
	releaseSecond, err := packageStore.ReserveCapacity(500, 100, 20)
	if err != nil {
		t.Fatalf("ReserveCapacity() after release error = %v", err)
	}
	releaseSecond()
}

func TestCleanupStaleTemporaryRemovesOnlyOldRegularPartFiles(t *testing.T) {
	root := t.TempDir()
	tempRoot := filepath.Join(root, ".temporary")
	packageStore, err := New(filepath.Join(root, "packages"), tempRoot)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	now := time.Date(2026, time.August, 27, 12, 0, 0, 0, time.UTC)
	oldPart := filepath.Join(tempRoot, ".Old.pkg.123.part")
	newPart := filepath.Join(tempRoot, ".New.pkg.456.part")
	unrelated := filepath.Join(tempRoot, "operator-note.txt")
	for _, path := range []string{oldPart, newPart, unrelated} {
		if err := os.WriteFile(path, []byte("test"), 0o600); err != nil {
			t.Fatalf("WriteFile(%q) error = %v", path, err)
		}
	}
	if err := os.Chtimes(oldPart, now.Add(-2*time.Hour), now.Add(-2*time.Hour)); err != nil {
		t.Fatalf("Chtimes() error = %v", err)
	}
	if err := os.Chtimes(newPart, now.Add(-30*time.Minute), now.Add(-30*time.Minute)); err != nil {
		t.Fatalf("Chtimes() error = %v", err)
	}

	removed, err := packageStore.CleanupStaleTemporary(time.Hour, now)
	if err != nil {
		t.Fatalf("CleanupStaleTemporary() error = %v", err)
	}
	if removed != 1 {
		t.Fatalf("removed = %d, want 1", removed)
	}
	if _, err := os.Stat(oldPart); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("old part still exists, error = %v", err)
	}
	for _, path := range []string{newPart, unrelated} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("expected retained file %q: %v", path, err)
		}
	}
}

func TestOpenRejectsSymlinkWithoutFollowingIt(t *testing.T) {
	root := t.TempDir()
	storeRoot := filepath.Join(root, "packages")
	packageStore, err := New(storeRoot, filepath.Join(root, ".temporary"))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	target := filepath.Join(root, "outside.pkg")
	if err := os.WriteFile(target, []byte("outside content"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	if err := os.Symlink(target, filepath.Join(storeRoot, "Linked.pkg")); err != nil {
		t.Fatalf("Symlink() error = %v", err)
	}

	file, _, err := packageStore.Open("Linked.pkg")
	if file != nil {
		file.Close()
		t.Fatal("Open() returned a file for a symbolic link")
	}
	if err == nil || errors.Is(err, ErrNotFound) {
		t.Fatalf("Open() error = %v, want a non-regular-file rejection", err)
	}
}

func TestEnsureDirectoryAcceptsWritablePreprovisionedDirectoryWhenChmodIsNotPermitted(t *testing.T) {
	path := filepath.Join(t.TempDir(), "packages")
	if err := os.Mkdir(path, 0o775); err != nil {
		t.Fatalf("Mkdir() error = %v", err)
	}

	err := ensureDirectoryWith(path, 0o755, func(string, os.FileMode) error {
		return syscall.EPERM
	})
	if err != nil {
		t.Fatalf("ensureDirectoryWith() error = %v", err)
	}
	entries, err := os.ReadDir(path)
	if err != nil {
		t.Fatalf("ReadDir() error = %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("write probe was not removed: %v", entries)
	}
}

func TestEnsureDirectoryRejectsWorldAccessiblePrivateDirectoryWhenChmodIsNotPermitted(t *testing.T) {
	path := filepath.Join(t.TempDir(), "temporary")
	if err := os.Mkdir(path, 0o777); err != nil {
		t.Fatalf("Mkdir() error = %v", err)
	}
	if err := os.Chmod(path, 0o777); err != nil {
		t.Fatalf("Chmod() error = %v", err)
	}

	err := ensureDirectoryWith(path, 0o700, func(string, os.FileMode) error {
		return syscall.EPERM
	})
	if err == nil || !strings.Contains(err.Error(), "expose a private path") {
		t.Fatalf("ensureDirectoryWith() error = %v, want private-path rejection", err)
	}
}

func TestEnsureDirectoryDoesNotIgnoreOtherChmodErrors(t *testing.T) {
	path := filepath.Join(t.TempDir(), "packages")
	want := errors.New("synthetic chmod failure")
	err := ensureDirectoryWith(path, 0o755, func(string, os.FileMode) error {
		return want
	})
	if !errors.Is(err, want) {
		t.Fatalf("ensureDirectoryWith() error = %v, want %v", err, want)
	}
}
