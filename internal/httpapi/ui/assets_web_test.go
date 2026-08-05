//go:build webembed

package ui

import (
	"bytes"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path"
	"testing"
)

func TestWebBuildOutputIsEmbedded(t *testing.T) {
	t.Parallel()

	buildOutput := os.DirFS("dist")
	handler := SPAHandler()
	fileCount := 0
	err := fs.WalkDir(buildOutput, ".", func(name string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		fileCount++

		expected, err := fs.ReadFile(buildOutput, name)
		if err != nil {
			return err
		}
		request := httptest.NewRequest(http.MethodGet, "/"+path.Clean(name), nil)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusOK {
			t.Errorf("GET /%s status = %d, want %d", name, response.Code, http.StatusOK)
			return nil
		}
		if !bytes.Equal(response.Body.Bytes(), expected) {
			t.Errorf("GET /%s did not return the built asset", name)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if fileCount == 0 {
		t.Fatal("web build output contains no files")
	}
}
