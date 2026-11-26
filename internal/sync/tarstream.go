package sync

import (
	"archive/tar"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// StreamDirAsTar writes a tar stream of dirPath (excluding symlinks) up to maxBytes.
func StreamDirAsTar(w io.Writer, dirPath string, maxBytes int64) error {
	info, err := os.Stat(dirPath)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return fmt.Errorf("not a directory: %s", dirPath)
	}

	tw := tar.NewWriter(w)
	defer tw.Close()

	var totalBytes int64
	return filepath.WalkDir(dirPath, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if path == dirPath {
			return nil
		}
		// Skip symlinks for safety
		if d.Type()&os.ModeSymlink != 0 {
			return nil
		}
		rel, err := filepath.Rel(dirPath, path)
		if err != nil {
			return err
		}
		relSlash := filepath.ToSlash(rel)
		fi, err := d.Info()
		if err != nil {
			return err
		}

		hdr, err := tar.FileInfoHeader(fi, "")
		if err != nil {
			return err
		}
		hdr.Name = relSlash
		if fi.IsDir() && !strings.HasSuffix(hdr.Name, "/") {
			hdr.Name += "/"
		}
		if err := tw.WriteHeader(hdr); err != nil {
			return err
		}
		if fi.Mode().IsRegular() {
			totalBytes += fi.Size()
			if maxBytes > 0 && totalBytes > maxBytes {
				return fmt.Errorf("directory exceeds max transfer size (%d bytes)", maxBytes)
			}
			f, err := os.Open(path)
			if err != nil {
				return err
			}
			_, copyErr := io.Copy(tw, io.LimitReader(f, fi.Size()))
			_ = f.Close()
			if copyErr != nil {
				return copyErr
			}
		}
		return nil
	})
}

// ExtractTarSafely extracts a tar stream into destDir with strict Zip-Slip and quota protection.
func ExtractTarSafely(r io.Reader, destDir string, maxBytes int64) error {
	if err := os.MkdirAll(destDir, 0o700); err != nil {
		return err
	}
	cleanDest := filepath.Clean(destDir)
	tr := tar.NewReader(r)

	var totalRead int64
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return err
		}

		name := strings.TrimSpace(hdr.Name)
		if name == "" || name == "." || name == "./" {
			continue
		}
		if strings.HasPrefix(name, "/") || strings.HasPrefix(name, `\`) || filepath.IsAbs(name) {
			return fmt.Errorf("unsafe absolute tar path: %s", hdr.Name)
		}
		// Reject Windows drive prefix like C:
		if len(name) >= 2 && name[1] == ':' {
			return fmt.Errorf("unsafe drive tar path: %s", hdr.Name)
		}

		cleanRel := filepath.Clean(filepath.FromSlash(name))
		if cleanRel == ".." || strings.HasPrefix(cleanRel, ".."+string(os.PathSeparator)) {
			return fmt.Errorf("zip-slip path rejected: %s", hdr.Name)
		}
		target := filepath.Join(cleanDest, cleanRel)
		if target != cleanDest && !strings.HasPrefix(target, cleanDest+string(os.PathSeparator)) {
			return fmt.Errorf("path escape rejected: %s", hdr.Name)
		}

		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o700); err != nil {
				return err
			}
		case tar.TypeReg:
			if hdr.Size < 0 {
				return fmt.Errorf("invalid file size in tar: %d", hdr.Size)
			}
			totalRead += hdr.Size
			if maxBytes > 0 && totalRead > maxBytes {
				return fmt.Errorf("archive exceeds max allowed size (%d bytes)", maxBytes)
			}
			if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
				return err
			}
			out, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
			if err != nil {
				return err
			}
			n, copyErr := io.Copy(out, io.LimitReader(tr, hdr.Size))
			closeErr := out.Close()
			if copyErr != nil {
				return copyErr
			}
			if closeErr != nil {
				return closeErr
			}
			if n != hdr.Size {
				return fmt.Errorf("truncated file %s in tar", hdr.Name)
			}
		default:
			// Ignore symlinks, hardlinks, fifos, devices for security
			continue
		}
	}
	return nil
}
