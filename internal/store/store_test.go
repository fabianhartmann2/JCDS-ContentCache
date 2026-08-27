package store

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
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
