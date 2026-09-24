package annotation

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

var excludedEvidenceDirs = map[string]struct{}{
	".git": {}, ".hg": {}, ".svn": {},
	"node_modules": {}, "__pycache__": {}, ".cache": {},
}

type evidenceEntry struct {
	absPath string
	relPath string
	info    fs.FileInfo
	target  string
}

// TreeHash returns a deterministic SHA-256 of selected paths, file contents,
// symlink targets, entry types, and Unix permission bits.
func TreeHash(ctx context.Context, path string) (string, error) {
	entries, err := collectEvidenceEntries(ctx, path)
	if err != nil {
		return "", err
	}
	hash := sha256.New()
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		kind := byte('f')
		switch {
		case entry.info.IsDir():
			kind = 'd'
		case entry.info.Mode()&os.ModeSymlink != 0:
			kind = 'l'
		}
		hash.Write([]byte{kind, 0})
		hash.Write([]byte(filepath.ToSlash(entry.relPath)))
		hash.Write([]byte{0})
		hash.Write([]byte(strconv.FormatUint(uint64(entry.info.Mode().Perm()), 8)))
		hash.Write([]byte{0})
		if kind == 'l' {
			hash.Write([]byte(entry.target))
		} else if kind == 'f' {
			if err := hashEvidenceFile(ctx, hash, entry.absPath); err != nil {
				return "", err
			}
		}
		hash.Write([]byte{'\n'})
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

// CopyEvidenceTree creates an immutable-style evidence copy at a new path and
// verifies that its selected tree hash matches the source snapshot.
func CopyEvidenceTree(ctx context.Context, src, dst string) (hash string, err error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	srcAbs, err := filepath.Abs(src)
	if err != nil {
		return "", err
	}
	dstAbs, err := filepath.Abs(dst)
	if err != nil {
		return "", err
	}
	if pathWithin(srcAbs, dstAbs) {
		return "", fmt.Errorf("evidence destination %q must not be inside source %q", dstAbs, srcAbs)
	}
	if _, err := os.Lstat(dstAbs); err == nil {
		return "", fmt.Errorf("evidence destination already exists: %s", dstAbs)
	} else if !os.IsNotExist(err) {
		return "", err
	}

	sourceInfo, err := os.Stat(srcAbs)
	if err != nil {
		return "", fmt.Errorf("inspect evidence source: %w", err)
	}
	if !sourceInfo.IsDir() {
		return "", fmt.Errorf("evidence source is not a directory: %s", srcAbs)
	}
	sourceHash, err := TreeHash(ctx, srcAbs)
	if err != nil {
		return "", err
	}
	entries, err := collectEvidenceEntries(ctx, srcAbs)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(dstAbs), 0755); err != nil {
		return "", err
	}
	if err := os.Mkdir(dstAbs, sourceInfo.Mode().Perm()); err != nil {
		return "", err
	}
	if err := os.Chmod(dstAbs, sourceInfo.Mode().Perm()); err != nil {
		return "", err
	}
	created := true
	defer func() {
		if err != nil && created {
			_ = os.RemoveAll(dstAbs)
		}
	}()

	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		targetPath := filepath.Join(dstAbs, entry.relPath)
		switch {
		case entry.info.IsDir():
			if err := os.Mkdir(targetPath, entry.info.Mode().Perm()); err != nil {
				return "", err
			}
			if err := os.Chmod(targetPath, entry.info.Mode().Perm()); err != nil {
				return "", err
			}
		case entry.info.Mode()&os.ModeSymlink != 0:
			if err := os.Symlink(entry.target, targetPath); err != nil {
				return "", err
			}
		default:
			if err := copyEvidenceFile(ctx, entry.absPath, targetPath, entry.info.Mode().Perm()); err != nil {
				return "", err
			}
		}
	}
	copiedHash, err := TreeHash(ctx, dstAbs)
	if err != nil {
		return "", err
	}
	if copiedHash != sourceHash {
		return "", fmt.Errorf("evidence source changed while copying: source %s, copy %s", sourceHash, copiedHash)
	}
	created = false
	return sourceHash, nil
}

func collectEvidenceEntries(ctx context.Context, root string) ([]evidenceEntry, error) {
	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	rootInfo, err := os.Lstat(rootAbs)
	if err != nil {
		return nil, err
	}
	if !rootInfo.IsDir() || rootInfo.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("evidence root must be a directory and not a symlink: %s", rootAbs)
	}
	rootReal, err := filepath.EvalSymlinks(rootAbs)
	if err != nil {
		return nil, err
	}

	entries := make([]evidenceEntry, 0)
	err = filepath.WalkDir(rootAbs, func(path string, item fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if path == rootAbs {
			return nil
		}
		if _, excluded := excludedEvidenceDirs[item.Name()]; excluded {
			if item.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		rel, err := filepath.Rel(rootAbs, path)
		if err != nil {
			return err
		}
		info, err := item.Info()
		if err != nil {
			return err
		}
		entry := evidenceEntry{absPath: path, relPath: rel, info: info}
		switch {
		case info.IsDir(), info.Mode().IsRegular():
		case info.Mode()&os.ModeSymlink != 0:
			target, err := os.Readlink(path)
			if err != nil {
				return err
			}
			if filepath.IsAbs(target) {
				return fmt.Errorf("unsafe absolute symlink %s -> %s", rel, target)
			}
			lexicalTarget := filepath.Clean(filepath.Join(filepath.Dir(path), target))
			if !pathWithin(rootAbs, lexicalTarget) {
				return fmt.Errorf("unsafe symlink outside evidence root: %s -> %s", rel, target)
			}
			if resolved, err := filepath.EvalSymlinks(path); err == nil {
				if !pathWithin(rootReal, resolved) {
					return fmt.Errorf("unsafe symlink outside evidence root: %s -> %s", rel, target)
				}
			} else if !os.IsNotExist(err) {
				return fmt.Errorf("resolve evidence symlink %s: %w", rel, err)
			}
			entry.target = target
		default:
			return fmt.Errorf("unsupported evidence file type at %s", rel)
		}
		entries = append(entries, entry)
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(entries, func(i, j int) bool {
		return filepath.ToSlash(entries[i].relPath) < filepath.ToSlash(entries[j].relPath)
	})
	return entries, nil
}

func pathWithin(root, candidate string) bool {
	rel, err := filepath.Rel(root, candidate)
	if err != nil {
		return false
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)))
}

func hashEvidenceFile(ctx context.Context, target io.Writer, path string) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	buffer := make([]byte, 128*1024)
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		read, readErr := file.Read(buffer)
		if read > 0 {
			if _, err := target.Write(buffer[:read]); err != nil {
				return err
			}
		}
		if readErr == io.EOF {
			return nil
		}
		if readErr != nil {
			return readErr
		}
	}
}

func copyEvidenceFile(ctx context.Context, src, dst string, mode os.FileMode) (err error) {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
	if err != nil {
		return err
	}
	if err := os.Chmod(dst, mode); err != nil {
		_ = out.Close()
		return err
	}
	defer func() {
		closeErr := out.Close()
		if err == nil {
			err = closeErr
		}
	}()
	buffer := make([]byte, 128*1024)
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		read, readErr := in.Read(buffer)
		if read > 0 {
			if _, err := out.Write(buffer[:read]); err != nil {
				return err
			}
		}
		if readErr == io.EOF {
			return nil
		}
		if readErr != nil {
			return readErr
		}
	}
}
