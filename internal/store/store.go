package store

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

var ErrNotFound = errors.New("package is not stored locally")

type Store struct {
	root     string
	tempRoot string
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

	return &Store{root: filepath.Clean(root), tempRoot: filepath.Clean(tempRoot)}, nil
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
	return os.Chmod(path, permissions)
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
