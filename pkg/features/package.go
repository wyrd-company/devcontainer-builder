// relationships:
//
//	references: [features, ../../reference/cli/src/spec-node/collectionCommonUtils/packageCommandImpl]
package features

import (
	"archive/tar"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/wyrd-company/devcontainer-builder/pkg/spec"
)

// PackageOptions selects local Feature sources and the packaging destination.
// Empty Target and OutputDir mean the current folder and ./output, respectively.
type PackageOptions struct {
	Target, OutputDir string
	// ForceCleanOutputDir removes an existing output directory before packaging.
	ForceCleanOutputDir bool
}

// Collection is the devcontainer-collection.json written by Package. Raw Feature
// metadata retains properties that this library does not otherwise interpret.
type Collection struct {
	SourceInformation struct {
		Source string `json:"source"`
	} `json:"sourceInformation"`
	Features []json.RawMessage `json:"features"`
}

// Package writes a local Feature or collection as devcontainer-feature-*.tgz
// archives and devcontainer-collection.json, without changing its sources.
// Like upstream, the archives contain plain tar bytes despite the .tgz suffix.
// A single archive uses the metadata ID; collection archives use folder names.
// Errors can leave archives from preceding collection entries in OutputDir.
func Package(ctx context.Context, opts PackageOptions) (*Collection, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if opts.Target == "" {
		opts.Target = "."
	}
	if opts.OutputDir == "" {
		opts.OutputDir = "./output"
	}
	target, err := filepath.Abs(opts.Target)
	if err != nil {
		return nil, err
	}
	if !packageIsDir(target) {
		return nil, fmt.Errorf("target folder %q does not exist", target)
	}
	out, err := filepath.Abs(opts.OutputDir)
	if err != nil {
		return nil, err
	}
	if packageIsDir(out) {
		if !opts.ForceCleanOutputDir {
			return nil, fmt.Errorf("output directory %q already exists; remove it or pass --force-clean-output-folder", out)
		}
		if err := os.RemoveAll(out); err != nil {
			return nil, err
		}
	}
	if err := os.MkdirAll(out, 0o755); err != nil {
		return nil, err
	}
	collection := &Collection{}
	collection.SourceInformation.Source = "devcontainer-cli"
	if packageIsFile(filepath.Join(target, "devcontainer-feature.json")) {
		meta, err := packageFeature(ctx, target, out, "", false)
		if err != nil {
			return nil, err
		}
		collection.Features = append(collection.Features, meta)
	} else {
		entries, err := os.ReadDir(target)
		if err != nil {
			return nil, err
		}
		for _, entry := range entries {
			folder := filepath.Join(target, entry.Name())
			if strings.HasPrefix(entry.Name(), ".") || !packageIsFile(filepath.Join(folder, "devcontainer-feature.json")) {
				continue
			}
			meta, err := packageFeature(ctx, folder, out, entry.Name(), true)
			if err != nil {
				return nil, err
			}
			collection.Features = append(collection.Features, meta)
		}
	}
	if len(collection.Features) == 0 {
		return nil, fmt.Errorf("no Features to package in %q", target)
	}
	data, err := json.MarshalIndent(collection, "", "    ")
	if err != nil {
		return nil, err
	}
	if err := os.WriteFile(filepath.Join(out, "devcontainer-collection.json"), data, 0o644); err != nil {
		return nil, err
	}
	return collection, nil
}

func packageIsDir(path string) bool {
	s, err := os.Stat(path)
	return err == nil && s.IsDir()
}

func packageIsFile(path string) bool {
	s, err := os.Stat(path)
	return err == nil && s.Mode().IsRegular()
}

func packageFeature(ctx context.Context, source, out, name string, collection bool) (json.RawMessage, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	// Snapshot before creating an archive, including when output is inside source.
	tmp, err := os.MkdirTemp("", "dcb-package-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(tmp)
	staged := filepath.Join(tmp, "feature")
	if err := packageCopy(ctx, source, staged); err != nil {
		return nil, err
	}
	if collection && !packageIsFile(filepath.Join(staged, "install.sh")) {
		return nil, fmt.Errorf("Feature %q is missing an install.sh", name)
	}
	metaPath := filepath.Join(staged, "devcontainer-feature.json")
	data, err := os.ReadFile(metaPath)
	if err != nil {
		return nil, err
	}
	stripped := spec.StripJSONC(data)
	var meta spec.OrderedMap
	if err := json.Unmarshal(stripped, &meta); err != nil {
		return nil, fmt.Errorf("Feature metadata: %w", err)
	}
	var legacy []json.RawMessage
	_ = json.Unmarshal(meta.Values["legacyIds"], &legacy)
	if len(legacy) > 0 {
		if _, exists := meta.Values["currentId"]; !exists {
			meta.Keys = append(meta.Keys, "currentId")
		}
		meta.Values["currentId"] = meta.Values["id"]
		var ordered bytes.Buffer
		ordered.WriteByte('{')
		for i, key := range meta.Keys {
			if i > 0 {
				ordered.WriteByte(',')
			}
			k, _ := json.Marshal(key)
			ordered.Write(k)
			ordered.WriteByte(':')
			value := meta.Values[key]
			if len(value) == 0 {
				value = json.RawMessage("null")
			}
			ordered.Write(value)
		}
		ordered.WriteByte('}')
		var pretty bytes.Buffer
		if err := json.Indent(&pretty, ordered.Bytes(), "", "    "); err != nil {
			return nil, err
		}
		stripped = pretty.Bytes()
		// A copied metadata symlink must not send this write back to source.
		if info, err := os.Lstat(metaPath); err != nil {
			return nil, err
		} else if info.Mode()&fs.ModeSymlink != 0 {
			if err := os.Remove(metaPath); err != nil {
				return nil, err
			}
		}
		if err := os.WriteFile(metaPath, stripped, 0o644); err != nil {
			return nil, err
		}
	}
	var id, version, displayName string
	_ = json.Unmarshal(meta.Values["id"], &id)
	_ = json.Unmarshal(meta.Values["version"], &version)
	_ = json.Unmarshal(meta.Values["name"], &displayName)
	// Upstream validates a single Feature before archiving, a collection entry after.
	valid := id != "" && version != "" && displayName != ""
	if !collection && !valid {
		return nil, fmt.Errorf("Feature is missing id, version or name")
	}
	if name == "" {
		name = id
	}
	archive := filepath.Join(out, "devcontainer-feature-"+name+".tgz")
	relative, err := filepath.Rel(out, archive)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return nil, fmt.Errorf("Feature id %q would write outside output directory", id)
	}
	if err := packageTar(ctx, staged, archive); err != nil {
		return nil, err
	}
	if !valid {
		return nil, fmt.Errorf("Feature %q is missing id, version or name", name)
	}
	return stripped, nil
}

func packageCopy(ctx context.Context, source, dest string) error {
	// Follow the selected root, but preserve symlinks inside the Feature.
	source, err := filepath.EvalSymlinks(source)
	if err != nil {
		return err
	}
	return filepath.WalkDir(source, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		rel, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dest, rel)
		info, err := d.Info()
		if err != nil {
			return err
		}
		switch {
		case d.IsDir():
			return os.MkdirAll(target, info.Mode().Perm())
		case d.Type()&fs.ModeSymlink != 0:
			link, err := os.Readlink(path)
			if err != nil {
				return err
			}
			return os.Symlink(link, target)
		case info.Mode().IsRegular():
			return packageCopyFile(path, target, info.Mode().Perm())
		}
		return nil // ncp also ignores sockets, devices and FIFOs.
	})
}

func packageCopyFile(source, dest string, mode fs.FileMode) error {
	in, err := os.Open(source)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dest, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	_, err = io.Copy(out, in)
	return firstPackageError(err, out.Close())
}

func packageTar(ctx context.Context, source, archive string) error {
	f, err := os.Create(archive)
	if err != nil {
		return err
	}
	tw := tar.NewWriter(f)
	err = filepath.WalkDir(source, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		link := ""
		if d.Type()&fs.ModeSymlink != 0 {
			link, err = os.Readlink(path)
			if err != nil {
				return err
			}
		}
		h, err := tar.FileInfoHeader(info, link)
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		h.Name = "./" + filepath.ToSlash(rel)
		if rel == "." {
			h.Name = "./"
		} else if d.IsDir() {
			h.Name += "/"
		}
		if err := tw.WriteHeader(h); err != nil {
			return err
		}
		if info.Mode().IsRegular() {
			in, err := os.Open(path)
			if err != nil {
				return err
			}
			_, err = io.Copy(tw, in)
			return firstPackageError(err, in.Close())
		}
		return nil
	})
	err = firstPackageError(err, tw.Close())
	return firstPackageError(err, f.Close())
}

func firstPackageError(first, second error) error {
	if first != nil {
		return first
	}
	return second
}
