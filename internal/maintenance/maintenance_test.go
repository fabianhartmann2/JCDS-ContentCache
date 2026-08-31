package maintenance

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestParseAccessEvent(t *testing.T) {
	tests := []struct {
		name    string
		message string
		want    string
		ok      bool
	}{
		{"complete", `<190>prefix {"status":200,"uri":"/packages/Example.pkg"}`, "Example.pkg", true},
		{"range", `{"status":206,"uri":"/packages/Example%20File.pkg"}`, "Example File.pkg", true},
		{"miss", `{"status":404,"uri":"/packages/Example.pkg"}`, "", false},
		{"traversal", `{"status":200,"uri":"/packages/../secret.pkg"}`, "", false},
		{"health", `{"status":200,"uri":"/health/ready"}`, "", false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, ok := ParseAccessEvent([]byte(test.message))
			if got != test.want || ok != test.ok {
				t.Fatalf("ParseAccessEvent() = %q, %v; want %q, %v", got, ok, test.want, test.ok)
			}
		})
	}
}

func TestIndexPersistsNewestAccess(t *testing.T) {
	path := filepath.Join(t.TempDir(), "access-index.json")
	index, err := LoadIndex(path)
	if err != nil {
		t.Fatal(err)
	}
	newer := time.Date(2026, 8, 31, 7, 0, 0, 0, time.UTC)
	if err := index.Record("Example.pkg", newer); err != nil {
		t.Fatal(err)
	}
	if err := index.Record("Example.pkg", newer.Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := index.Flush(); err != nil {
		t.Fatal(err)
	}
	reloaded, err := LoadIndex(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := reloaded.snapshot()["Example.pkg"]; !got.Equal(newer) {
		t.Fatalf("persisted access = %v; want %v", got, newer)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("index mode = %04o; want 0600", info.Mode().Perm())
	}
}

func TestCleanerDeletesOldestEligibleFilesToTarget(t *testing.T) {
	root := t.TempDir()
	maintenanceRoot := t.TempDir()
	now := time.Date(2026, 8, 31, 7, 0, 0, 0, time.UTC)
	writePackage := func(name string, size int, age time.Duration) {
		path := filepath.Join(root, name)
		if err := os.WriteFile(path, []byte(strings.Repeat("x", size)), 0o644); err != nil {
			t.Fatal(err)
		}
		at := now.Add(-age)
		if err := os.Chtimes(path, at, at); err != nil {
			t.Fatal(err)
		}
	}
	writePackage("Oldest.pkg", 30, 120*24*time.Hour)
	writePackage("Old.pkg", 30, 100*24*time.Hour)
	writePackage("Recent.pkg", 80, 10*24*time.Hour)
	if err := os.Symlink(filepath.Join(root, "Oldest.pkg"), filepath.Join(root, "Link.pkg")); err != nil {
		t.Fatal(err)
	}
	index, err := LoadIndex(filepath.Join(maintenanceRoot, "index.json"))
	if err != nil {
		t.Fatal(err)
	}
	cleaner := &Cleaner{
		StoreRoot:      root,
		AuditPath:      filepath.Join(maintenanceRoot, "audit.jsonl"),
		Retention:      90 * 24 * time.Hour,
		TriggerPercent: 30,
		TargetPercent:  35,
		Index:          index,
		Now:            func() time.Time { return now },
		Space:          func(string) (int64, int64, error) { return 290, 1000, nil },
	}
	result, err := cleaner.Run()
	if err != nil {
		t.Fatal(err)
	}
	if !result.Triggered || result.RemovedFiles != 2 || result.RemovedBytes != 60 {
		t.Fatalf("unexpected cleanup result: %+v", result)
	}
	for _, name := range []string{"Oldest.pkg", "Old.pkg"} {
		if _, err := os.Stat(filepath.Join(root, name)); !os.IsNotExist(err) {
			t.Fatalf("%s was not removed", name)
		}
	}
	for _, name := range []string{"Recent.pkg", "Link.pkg"} {
		if _, err := os.Lstat(filepath.Join(root, name)); err != nil {
			t.Fatalf("%s should remain: %v", name, err)
		}
	}
}

func TestCleanerDoesNothingAtTriggerBoundary(t *testing.T) {
	root := t.TempDir()
	index, err := LoadIndex(filepath.Join(t.TempDir(), "index.json"))
	if err != nil {
		t.Fatal(err)
	}
	cleaner := &Cleaner{
		StoreRoot:      root,
		AuditPath:      filepath.Join(t.TempDir(), "audit.jsonl"),
		Retention:      time.Hour,
		TriggerPercent: 30,
		TargetPercent:  35,
		Index:          index,
		Space:          func(string) (int64, int64, error) { return 300, 1000, nil },
	}
	result, err := cleaner.Run()
	if err != nil {
		t.Fatal(err)
	}
	if result.Triggered {
		t.Fatal("cleanup triggered at the boundary")
	}
}
