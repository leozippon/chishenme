package server

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"mime"
	"net/http"
	"path"
	"strconv"
	"strings"
)

// staticHandler serves the embedded web app. The files are hashed once so a
// reload after a deploy still picks up a changed page.
type staticHandler struct {
	files map[string]staticFile
}

type staticFile struct {
	data        []byte
	etag        string
	contentType string
}

func newStatic(fsys fs.FS) (http.Handler, error) {
	h := &staticHandler{files: map[string]staticFile{}}
	err := fs.WalkDir(fsys, ".", func(name string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		data, err := fs.ReadFile(fsys, name)
		if err != nil {
			return err
		}
		sum := sha256.Sum256(data)
		h.files["/"+name] = staticFile{
			data:        data,
			etag:        `"` + hex.EncodeToString(sum[:12]) + `"`,
			contentType: contentType(name),
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("load web assets: %w", err)
	}
	index, ok := h.files["/index.html"]
	if !ok {
		return nil, errors.New("load web assets: index.html is missing")
	}
	h.files["/"] = index
	if icon, ok := h.files["/icon-180.png"]; ok {
		h.files["/apple-touch-icon.png"] = icon
		h.files["/apple-touch-icon-precomposed.png"] = icon
	}
	return h, nil
}

func (h *staticHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	file, ok := h.files[r.URL.Path]
	if !ok {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", file.contentType)
	w.Header().Set("ETag", file.etag)
	w.Header().Set("Cache-Control", "no-cache")
	if strings.Contains(r.Header.Get("If-None-Match"), file.etag) {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	w.Header().Set("Content-Length", strconv.Itoa(len(file.data)))
	if r.Method == http.MethodHead {
		return
	}
	w.Write(file.data)
}

func contentType(name string) string {
	switch path.Ext(name) {
	case ".html":
		return "text/html; charset=utf-8"
	case ".css":
		return "text/css; charset=utf-8"
	case ".js":
		return "text/javascript; charset=utf-8"
	case ".svg":
		return "image/svg+xml"
	case ".webmanifest":
		return "application/manifest+json"
	}
	if ct := mime.TypeByExtension(path.Ext(name)); ct != "" {
		return ct
	}
	return "application/octet-stream"
}
