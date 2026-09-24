package annotation

import (
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

//go:embed assets/export_satisfaction.py assets/export_pairwise.py assets/check_submission.py assets/template.xlsx assets/skill
var bundledAssets embed.FS

var (
	assetsHashOnce sync.Once
	assetsHash     string
)

// AssetsHash returns the content hash of every bundled annotation asset.
func AssetsHash() string {
	assetsHashOnce.Do(func() {
		paths := make([]string, 0)
		if err := fs.WalkDir(bundledAssets, "assets", func(path string, entry fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if !entry.IsDir() {
				paths = append(paths, path)
			}
			return nil
		}); err != nil {
			panic(fmt.Sprintf("walk embedded annotation assets: %v", err))
		}
		sort.Strings(paths)
		digest := sha256.New()
		for _, path := range paths {
			content, err := bundledAssets.ReadFile(path)
			if err != nil {
				panic(fmt.Sprintf("read embedded annotation asset %q: %v", path, err))
			}
			digest.Write([]byte(path))
			digest.Write([]byte{0})
			digest.Write(content)
			digest.Write([]byte{0})
		}
		assetsHash = hex.EncodeToString(digest.Sum(nil))
	})
	return assetsHash
}

// MaterializeAssets extracts the bundled exporter, checker, template, and skill
// snapshot below an absolute application-data root. Existing version directories
// are reused only after every extracted file is hashed again.
func MaterializeAssets(root string) (string, error) {
	if strings.TrimSpace(root) == "" || !filepath.IsAbs(root) {
		return "", fmt.Errorf("annotation asset root must be an absolute path")
	}
	hash := AssetsHash()
	parent := filepath.Join(filepath.Clean(root), "annotation-assets")
	target := filepath.Join(parent, hash)
	if err := os.MkdirAll(parent, 0o700); err != nil {
		return "", fmt.Errorf("create annotation asset parent: %w", err)
	}
	if valid, err := materializedAssetsMatch(target, hash); err != nil {
		return "", err
	} else if valid {
		return target, nil
	}
	restored, err := filepath.Glob(target + "-restored-*")
	if err != nil {
		return "", fmt.Errorf("list restored annotation assets: %w", err)
	}
	sort.Strings(restored)
	for _, candidate := range restored {
		if valid, matchErr := materializedAssetsMatch(candidate, hash); matchErr != nil {
			return "", matchErr
		} else if valid {
			return candidate, nil
		}
	}
	staging, err := os.MkdirTemp(parent, ".extract-"+hash[:12]+"-")
	if err != nil {
		return "", fmt.Errorf("create annotation asset staging directory: %w", err)
	}
	keepStaging := false
	defer func() {
		if !keepStaging {
			_ = os.RemoveAll(staging)
		}
	}()

	source, err := fs.Sub(bundledAssets, "assets")
	if err != nil {
		return "", fmt.Errorf("open embedded annotation assets: %w", err)
	}
	if err := fs.WalkDir(source, ".", func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if path == "." {
			return nil
		}
		destination := filepath.Join(staging, filepath.FromSlash(path))
		relative, err := filepath.Rel(staging, destination)
		if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			return fmt.Errorf("unsafe embedded annotation asset path: %q", path)
		}
		if entry.IsDir() {
			return os.MkdirAll(destination, 0o700)
		}
		content, err := fs.ReadFile(source, path)
		if err != nil {
			return err
		}
		mode := fs.FileMode(0o600)
		if strings.HasSuffix(strings.ToLower(path), ".py") {
			mode = 0o700
		}
		if err := os.MkdirAll(filepath.Dir(destination), 0o700); err != nil {
			return err
		}
		return os.WriteFile(destination, content, mode)
	}); err != nil {
		return "", fmt.Errorf("extract annotation assets: %w", err)
	}
	if err := os.WriteFile(filepath.Join(staging, ".complete"), []byte(hash+"\n"), 0o600); err != nil {
		return "", fmt.Errorf("write annotation asset completion marker: %w", err)
	}
	if valid, err := materializedAssetsMatch(staging, hash); err != nil {
		return "", err
	} else if !valid {
		return "", errors.New("newly extracted annotation assets failed content verification")
	}
	publishTarget := target
	if _, err := os.Stat(target); err == nil {
		reserved, reserveErr := os.MkdirTemp(parent, hash+"-restored-")
		if reserveErr != nil {
			return "", fmt.Errorf("reserve restored annotation asset directory: %w", reserveErr)
		}
		if removeErr := os.Remove(reserved); removeErr != nil {
			return "", fmt.Errorf("prepare restored annotation asset directory: %w", removeErr)
		}
		publishTarget = reserved
	} else if !os.IsNotExist(err) {
		return "", fmt.Errorf("inspect annotation asset directory: %w", err)
	}
	if err := os.Rename(staging, publishTarget); err != nil {
		if valid, matchErr := materializedAssetsMatch(target, hash); matchErr == nil && valid {
			return target, nil
		}
		return "", fmt.Errorf("publish annotation assets: %w", err)
	}
	keepStaging = true
	return publishTarget, nil
}

func materializedAssetsMatch(root, expected string) (bool, error) {
	marker, err := os.ReadFile(filepath.Join(root, ".complete"))
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("read annotation asset completion marker: %w", err)
	}
	if strings.TrimSpace(string(marker)) != expected {
		return false, nil
	}
	paths := make([]string, 0)
	invalid := false
	err = filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.Type()&os.ModeSymlink != 0 {
			invalid = true
			return nil
		}
		if entry.IsDir() || path == filepath.Join(root, ".complete") {
			return nil
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		paths = append(paths, "assets/"+filepath.ToSlash(relative))
		return nil
	})
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("inspect materialized annotation assets: %w", err)
	}
	if invalid {
		return false, nil
	}
	sort.Strings(paths)
	digest := sha256.New()
	for _, logicalPath := range paths {
		relative := strings.TrimPrefix(logicalPath, "assets/")
		content, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(relative)))
		if err != nil {
			return false, fmt.Errorf("read materialized annotation asset %q: %w", logicalPath, err)
		}
		digest.Write([]byte(logicalPath))
		digest.Write([]byte{0})
		digest.Write(content)
		digest.Write([]byte{0})
	}
	return hex.EncodeToString(digest.Sum(nil)) == expected, nil
}
