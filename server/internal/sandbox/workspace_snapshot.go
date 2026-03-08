package sandbox

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const snapshotMetaFileName = ".hwr_snapshot_meta"

func ensureDir(path string) error {
	return os.MkdirAll(path, 0o755)
}

func snapshotDir(rootDir, sandboxID, snapshotID string) string {
	return filepath.Join(rootDir, sandboxID, snapshotID)
}

func cloneSnapshotBase(baseDir, targetDir string) error {
	if strings.TrimSpace(baseDir) == "" {
		return nil
	}
	if _, err := os.Stat(baseDir); err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	return filepath.Walk(baseDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(baseDir, path)
		if err != nil {
			return err
		}
		if rel == "." || rel == snapshotMetaFileName {
			return nil
		}
		dst := filepath.Join(targetDir, rel)
		if info.IsDir() {
			return os.MkdirAll(dst, info.Mode())
		}
		if info.Mode()&os.ModeSymlink != 0 {
			linkTarget, err := os.Readlink(path)
			if err != nil {
				return err
			}
			_ = os.RemoveAll(dst)
			return os.Symlink(linkTarget, dst)
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return err
		}
		_ = os.RemoveAll(dst)
		return os.Link(path, dst)
	})
}

func syncTree(srcRoot, dstRoot string) error {
	seen := map[string]struct{}{}
	if err := filepath.Walk(srcRoot, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(srcRoot, path)
		if err != nil {
			return err
		}
		if rel == "." || rel == snapshotMetaFileName {
			return nil
		}
		seen[rel] = struct{}{}

		dst := filepath.Join(dstRoot, rel)
		if info.IsDir() {
			return os.MkdirAll(dst, info.Mode())
		}
		if info.Mode()&os.ModeSymlink != 0 {
			linkTarget, err := os.Readlink(path)
			if err != nil {
				return err
			}
			_ = os.RemoveAll(dst)
			return os.Symlink(linkTarget, dst)
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		dstInfo, statErr := os.Stat(dst)
		if statErr == nil && dstInfo.Mode().IsRegular() &&
			dstInfo.Size() == info.Size() && dstInfo.ModTime().Equal(info.ModTime()) {
			return nil
		}
		if err := copyRegularFile(path, dst, info.Mode(), info.ModTime()); err != nil {
			return err
		}
		return nil
	}); err != nil {
		return err
	}

	type stalePath struct {
		path  string
		isDir bool
	}
	stales := make([]stalePath, 0)
	if err := filepath.Walk(dstRoot, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(dstRoot, path)
		if err != nil {
			return err
		}
		if rel == "." || rel == snapshotMetaFileName {
			return nil
		}
		if _, ok := seen[rel]; ok {
			return nil
		}
		stales = append(stales, stalePath{
			path:  path,
			isDir: info.IsDir(),
		})
		return nil
	}); err != nil {
		return err
	}

	for i := len(stales) - 1; i >= 0; i-- {
		if stales[i].isDir {
			if err := os.Remove(stales[i].path); err != nil && !os.IsNotExist(err) {
				return err
			}
			continue
		}
		if err := os.Remove(stales[i].path); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	return nil
}

func copyRegularFile(src, dst string, perm os.FileMode, modTime time.Time) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	_ = os.RemoveAll(dst)
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, perm)
	if err != nil {
		return err
	}
	defer out.Close()

	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	if err := out.Sync(); err != nil {
		return err
	}
	return os.Chtimes(dst, modTime, modTime)
}

func writeSnapshotMeta(dir, sandboxID, sourceHostPath, baseSnapshotID string) error {
	content := fmt.Sprintf(
		"sandbox_id=%s\nsource_host_path=%s\nbase_snapshot_id=%s\n",
		sandboxID,
		sourceHostPath,
		baseSnapshotID,
	)
	return os.WriteFile(filepath.Join(dir, snapshotMetaFileName), []byte(content), 0o644)
}
