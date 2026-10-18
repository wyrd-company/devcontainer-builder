package features

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

func extractTarMaybeGz(data []byte, dir string) error {
	var rdr io.Reader = bytes.NewReader(data)
	if len(data) > 2 && data[0] == 0x1f && data[1] == 0x8b {
		gz, err := gzip.NewReader(bytes.NewReader(data))
		if err != nil {
			return fmt.Errorf("gzip: %w", err)
		}
		defer gz.Close()
		rdr = gz
	}
	tr := tar.NewReader(rdr)
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("tar: %w", err)
		}
		// Sanitize: refuse paths that escape the destination.
		clean := filepath.Clean(hdr.Name)
		if clean == "." {
			// The archive root ("./" or "."), which is the destination
			// itself. Published features carry this entry.
			continue
		}
		if clean == ".." || strings.HasPrefix(clean, "../") || filepath.IsAbs(clean) {
			return fmt.Errorf("tar: unsafe path %q", hdr.Name)
		}
		target := filepath.Join(dir, clean)
		if err := ensureSafeTarget(dir, target); err != nil {
			return fmt.Errorf("tar: unsafe path %q: %w", hdr.Name, err)
		}
		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, fs.FileMode(hdr.Mode)|0o111); err != nil {
				return err
			}
		case tar.TypeReg, tar.TypeRegA:
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			f, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, fs.FileMode(hdr.Mode))
			if err != nil {
				return err
			}
			if _, err := io.Copy(f, tr); err != nil {
				f.Close()
				return err
			}
			f.Close()
		case tar.TypeSymlink:
			return fmt.Errorf("tar: symlinks are not supported in feature layers: %q -> %q", hdr.Name, hdr.Linkname)
		}
	}
}

func ensureSafeTarget(root, target string) error {
	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return err
	}
	rootReal, err := filepath.EvalSymlinks(rootAbs)
	if err != nil {
		return err
	}
	existingParent := filepath.Dir(target)
	for {
		if _, err := os.Lstat(existingParent); err == nil {
			break
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
		next := filepath.Dir(existingParent)
		if next == existingParent {
			return fmt.Errorf("no existing parent under staging root")
		}
		existingParent = next
	}
	parentReal, err := filepath.EvalSymlinks(existingParent)
	if err != nil {
		return err
	}
	rel, err := filepath.Rel(rootReal, parentReal)
	if err != nil {
		return err
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return fmt.Errorf("resolved parent escapes staging root")
	}
	if info, err := os.Lstat(target); err == nil && info.Mode()&fs.ModeSymlink != 0 {
		return fmt.Errorf("target is a symlink")
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

// safeIdent transforms a feature source and its canonical options into a
// path-safe identifier. It uses a sha256 prefix plus the trailing path
// component of the source to stay deterministic across invocations.
func safeIdent(s, options string) string {
	sum := sha256.Sum256([]byte(s + "\x00" + options))
	prefix := hex.EncodeToString(sum[:6])
	tail := s
	if i := strings.LastIndexAny(tail, "/:@"); i >= 0 {
		tail = tail[i+1:]
	}
	tail = strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z',
			r >= 'A' && r <= 'Z',
			r >= '0' && r <= '9',
			r == '-', r == '_':
			return r
		}
		return '-'
	}, tail)
	if tail == "" {
		tail = "feature"
	}
	return prefix + "-" + tail
}

func copyTree(src, dst string) error {
	if err := os.MkdirAll(dst, 0o755); err != nil {
		return err
	}
	return filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		switch {
		case d.IsDir():
			return os.MkdirAll(target, 0o755)
		case d.Type()&fs.ModeSymlink != 0:
			ln, err := os.Readlink(path)
			if err != nil {
				return err
			}
			_ = os.Remove(target)
			return os.Symlink(ln, target)
		default:
			info, err := d.Info()
			if err != nil {
				return err
			}
			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			return os.WriteFile(target, data, info.Mode())
		}
	})
}
