package ui

import (
	"io/fs"
	"net/http"
	"path"
	"strings"
)

// SPAHandler serves embedded assets and falls back to index.html for client-side
// routes. API routes are resolved before this handler is called.
func SPAHandler() http.HandlerFunc {
	content := assets()
	files := http.FileServer(http.FS(content))

	return func(response http.ResponseWriter, request *http.Request) {
		cleaned := path.Clean("/" + request.URL.Path)
		name := strings.TrimPrefix(cleaned, "/")
		if name == "." || name == "" {
			name = "index.html"
		}

		if name == "index.html" {
			serveIndex(response, content)
			return
		}
		if info, err := fs.Stat(content, name); err == nil && !info.IsDir() {
			files.ServeHTTP(response, request)
			return
		}

		serveIndex(response, content)
	}
}

func serveIndex(response http.ResponseWriter, content fs.FS) {
	index, err := fs.ReadFile(content, "index.html")
	if err != nil {
		http.Error(
			response,
			http.StatusText(http.StatusInternalServerError),
			http.StatusInternalServerError,
		)
		return
	}
	response.Header().Set("Cache-Control", "no-cache")
	response.Header().Set("Content-Type", "text/html; charset=utf-8")
	response.WriteHeader(http.StatusOK)
	_, _ = response.Write(index)
}
