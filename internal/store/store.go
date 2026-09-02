package store

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"
)

var (
	ErrNotFound          = errors.New("package is not stored locally")
	ErrInsufficientSpace = errors.New("package store has insufficient free space")
)

type Store struct {
	root          string
	tempRoot      string
	capacityMu    sync.Mutex
	reservedBytes int64
	space         func() (availableBytes int64, totalBytes int64, err error)
}

func New(root, tempRoot string) (*Store, error) {
	if !filepath.IsAbs(root) || !filepath.IsAbs(tempRoot) {
		return nil, errors.New("store and temporary roots must be absolute paths")
	}
	if filepath.Clean(root) == filepath.Clean(tempRoot) {
		return nil, errors.New("store and temporary roots must be different directories")
	}
	if err := ensureDirectory(root, 0o755); err != nil {
		return nil, fmt.Errorf("prepare package store: %w", err)
	}
	if err := ensureDirectory(tempRoot, 0o700); err != nil {
		return nil, fmt.Errorf("prepare temporary store: %w", err)
	}
	same, err := sameFilesystem(root, tempRoot)
	if err != nil {
		return nil, fmt.Errorf("compare store filesystems: %w", err)
	}
	if !same {
		return nil, errors.New("store and temporary roots must be on the same filesystem")
	}

	cleanRoot := filepath.Clean(root)
	cleanTempRoot := filepath.Clean(tempRoot)
	return &Store{
		root:     cleanRoot,
		tempRoot: cleanTempRoot,
		space: func() (int64, int64, error) {
			return filesystemSpace(cleanTempRoot)
		},
	}, nil
}

func (s *Store) Open(filename string) (*os.File, os.FileInfo, error) {
	finalPath, err := s.finalPath(filename)
	if err != nil {
		return nil, nil, err
	}
	info, err := os.Lstat(finalPath)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil, ErrNotFound
	}
	if err != nil {
		return nil, nil, fmt.Errorf("inspect local package: %w", err)
	}
	if !info.Mode().IsRegular() {
		return nil, nil, errors.New("local package path is not a regular file")
	}
	file, err := os.Open(finalPath)
	if err != nil {
		return nil, nil, fmt.Errorf("open local package: %w", err)
	}
	return file, info, nil
}

func (s *Store) Begin(filename string) (*Pending, error) {
	finalPath, err := s.finalPath(filename)
	if err != nil {
		return nil, err
	}
	file, err := os.CreateTemp(s.tempRoot, "."+filename+".*.part")
	if err != nil {
		return nil, fmt.Errorf("create temporary package: %w", err)
	}
	if err := file.Chmod(0o600); err != nil {
		file.Close()
		os.Remove(file.Name())
		return nil, fmt.Errorf("secure temporary package: %w", err)
	}
	return &Pending{
		file:      file,
		tempPath:  file.Name(),
		finalPath: finalPath,
	}, nil
}

func (s *Store) Ready() error {
	for _, directory := range []string{s.root, s.tempRoot} {
		info, err := os.Lstat(directory)
		if err != nil {
			return err
		}
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("%s is not a real directory", directory)
		}
	}
	return nil
}

// ReserveCapacity rejects a fill before any upstream object bytes are fetched
// when the expected package plus configured headroom would overcommit the
// package-store filesystem. Reservations are process-local and deliberately
// conservative: concurrent fills cannot each claim the same currently free
// space.
func (s *Store) ReserveCapacity(expectedBytes, minimumFreeBytes int64, minimumFreePercent float64) (func(), error) {
	if expectedBytes < 0 || minimumFreeBytes < 0 || minimumFreePercent < 0 || minimumFreePercent >= 100 {
		return nil, errors.New("invalid package-store capacity request")
	}

	s.capacityMu.Lock()
	availableBytes, totalBytes, err := s.space()
	if err != nil {
		s.capacityMu.Unlock()
		return nil, fmt.Errorf("inspect package-store capacity: %w", err)
	}
	percentReserve := int64(float64(totalBytes) * minimumFreePercent / 100)
	if percentReserve > minimumFreeBytes {
		minimumFreeBytes = percentReserve
	}
	remainingBytes := availableBytes - minimumFreeBytes
	if remainingBytes < 0 || s.reservedBytes > remainingBytes || expectedBytes > remainingBytes-s.reservedBytes {
		s.capacityMu.Unlock()
		return nil, fmt.Errorf(
			"%w: available=%d reserved=%d required=%d minimum_free=%d",
			ErrInsufficientSpace,
			availableBytes,
			s.reservedBytes,
			expectedBytes,
			minimumFreeBytes,
		)
	}
	s.reservedBytes += expectedBytes
	s.capacityMu.Unlock()

	var once sync.Once
	return func() {
		once.Do(func() {
			s.capacityMu.Lock()
			s.reservedBytes -= expectedBytes
			s.capacityMu.Unlock()
		})
	}, nil
}

// CleanupStaleTemporary removes only old regular .part files. It is intended
// for startup recovery, before this process can have active temporary files.
// Symlinks and unrelated entries are never followed or removed.
func (s *Store) CleanupStaleTemporary(maxAge time.Duration, now time.Time) (int, error) {
	if maxAge <= 0 {
		return 0, errors.New("temporary-file maximum age must be greater than zero")
	}
	entries, err := os.ReadDir(s.tempRoot)
	if err != nil {
		return 0, fmt.Errorf("read temporary package directory: %w", err)
	}

	removed := 0
	for _, entry := range entries {
		if !strings.HasSuffix(entry.Name(), ".part") {
			continue
		}
		path := filepath.Join(s.tempRoot, entry.Name())
		info, err := os.Lstat(path)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return removed, fmt.Errorf("inspect temporary package %q: %w", entry.Name(), err)
		}
		if !info.Mode().IsRegular() || now.Sub(info.ModTime()) < maxAge {
			continue
		}
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return removed, fmt.Errorf("remove stale temporary package %q: %w", entry.Name(), err)
		}
		removed++
	}
	return removed, nil
}

func (s *Store) finalPath(filename string) (string, error) {
	if err := ValidateFilename(filename); err != nil {
		return "", err
	}
	path := filepath.Join(s.root, filename)
	if filepath.Dir(path) != s.root {
		return "", errors.New("package path escaped the configured store root")
	}
	return path, nil
}

type Pending struct {
	file      *os.File
	tempPath  string
	finalPath string
	committed bool
}

func (p *Pending) Write(data []byte) (int, error) {
	return p.file.Write(data)
}

// OpenReader opens the growing temporary package for an independent in-flight
// reader. If publication won the race, it opens the atomically renamed final
// path instead.
func (p *Pending) OpenReader() (*os.File, error) {
	file, err := os.Open(p.tempPath)
	if err == nil {
		return file, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("open temporary package reader: %w", err)
	}
	file, err = os.Open(p.finalPath)
	if err != nil {
		return nil, fmt.Errorf("open published package reader: %w", err)
	}
	return file, nil
}

func (p *Pending) Commit(expectedBytes int64) error {
	if p.committed {
		return errors.New("temporary package has already been committed")
	}
	if err := p.file.Sync(); err != nil {
		return fmt.Errorf("sync temporary package: %w", err)
	}
	info, err := p.file.Stat()
	if err != nil {
		return fmt.Errorf("inspect temporary package: %w", err)
	}
	if expectedBytes >= 0 && info.Size() != expectedBytes {
		return fmt.Errorf("downloaded %d bytes, expected %d", info.Size(), expectedBytes)
	}
	if err := p.file.Chmod(0o644); err != nil {
		return fmt.Errorf("set completed package permissions: %w", err)
	}
	if err := p.file.Close(); err != nil {
		return fmt.Errorf("close completed package: %w", err)
	}
	if _, err := os.Lstat(p.finalPath); err == nil {
		return errors.New("final package path already exists")
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("inspect final package path: %w", err)
	}
	if err := os.Rename(p.tempPath, p.finalPath); err != nil {
		return fmt.Errorf("publish completed package: %w", err)
	}
	if err := syncDirectory(filepath.Dir(p.finalPath)); err != nil {
		return fmt.Errorf("sync package-store directory: %w", err)
	}
	p.committed = true
	return nil
}

func (p *Pending) Abort() error {
	if p.committed {
		return nil
	}
	_ = p.file.Close()
	if err := os.Remove(p.tempPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remove temporary package: %w", err)
	}
	return nil
}

func ValidateFilename(filename string) error {
	if len(filename) < len("a.pkg") || len(filename) > 255 {
		return errors.New("package filename length is invalid")
	}
	if !strings.HasSuffix(filename, ".pkg") {
		return errors.New("package filename must end in .pkg")
	}
	if !isASCIIAlphanumeric(filename[0]) {
		return errors.New("package filename must start with an ASCII letter or number")
	}
	for index := 1; index < len(filename); index++ {
		character := filename[index]
		if isASCIIAlphanumeric(character) || strings.ContainsRune("._+() -", rune(character)) {
			continue
		}
		return fmt.Errorf("package filename contains disallowed character at byte %d", index)
	}
	if filepath.Base(filename) != filename || strings.ContainsAny(filename, "/\\") {
		return errors.New("package filename must be one path segment")
	}
	return nil
}

func isASCIIAlphanumeric(character byte) bool {
	return character >= 'A' && character <= 'Z' ||
		character >= 'a' && character <= 'z' ||
		character >= '0' && character <= '9'
}

func ensureDirectory(path string, permissions os.FileMode) error {
	return ensureDirectoryWith(path, permissions, os.Chmod)
}

func ensureDirectoryWith(path string, permissions os.FileMode, chmod func(string, os.FileMode) error) error {
	if err := os.MkdirAll(path, permissions); err != nil {
		return err
	}
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("path is not a real directory")
	}
	if err := chmod(path, permissions); err == nil {
		return nil
	} else if !errors.Is(err, syscall.EPERM) {
		return err
	}

	// A platform initializer may own a pre-provisioned volume directory while
	// granting this non-root process access through its group. Docker Desktop
	// can reject chmod by that process even when the effective permissions are
	// already safe and writable. Accept only that EPERM case after validating
	// the actual mode and a create/remove probe; other chmod failures remain
	// fatal.
	info, err = os.Lstat(path)
	if err != nil {
		return err
	}
	if permissions&0o007 == 0 && info.Mode().Perm()&0o007 != 0 {
		return fmt.Errorf("directory permissions %04o expose a private path", info.Mode().Perm())
	}
	probe, err := os.CreateTemp(path, ".write-check-*")
	if err != nil {
		return fmt.Errorf("directory mode cannot be enforced and path is not writable: %w", err)
	}
	probePath := probe.Name()
	if closeErr := probe.Close(); closeErr != nil {
		_ = os.Remove(probePath)
		return fmt.Errorf("close directory write probe: %w", closeErr)
	}
	if err := os.Remove(probePath); err != nil {
		return fmt.Errorf("remove directory write probe: %w", err)
	}
	return nil
}

func sameFilesystem(first, second string) (bool, error) {
	firstInfo, err := os.Stat(first)
	if err != nil {
		return false, err
	}
	secondInfo, err := os.Stat(second)
	if err != nil {
		return false, err
	}
	firstStat, firstOK := firstInfo.Sys().(*syscall.Stat_t)
	secondStat, secondOK := secondInfo.Sys().(*syscall.Stat_t)
	if !firstOK || !secondOK {
		return false, errors.New("filesystem identity is unavailable")
	}
	return firstStat.Dev == secondStat.Dev, nil
}

func syncDirectory(path string) error {
	directory, err := os.Open(path)
	if err != nil {
		return err
	}
	defer directory.Close()
	return directory.Sync()
}

func filesystemSpace(path string) (int64, int64, error) {
	var statistics syscall.Statfs_t
	if err := syscall.Statfs(path, &statistics); err != nil {
		return 0, 0, err
	}
	if statistics.Bsize <= 0 {
		return 0, 0, errors.New("filesystem returned an invalid block size")
	}
	availableBytes := int64(statistics.Bavail) * statistics.Bsize
	totalBytes := int64(statistics.Blocks) * statistics.Bsize
	if availableBytes < 0 || totalBytes <= 0 {
		return 0, 0, errors.New("filesystem capacity exceeds the supported range")
	}
	return availableBytes, totalBytes, nil
}
