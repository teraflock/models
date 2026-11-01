package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestParseHFResolve(t *testing.T) {
	loc, ok := parseHFResolve("https://huggingface.co/unsloth/Qwen3-235B-GGUF/resolve/main/Q4_K_M/x-00001-of-00003.gguf")
	if !ok || loc.Repo != "unsloth/Qwen3-235B-GGUF" || loc.Rev != "main" || loc.Folder != "Q4_K_M" || loc.File != "x-00001-of-00003.gguf" {
		t.Fatalf("got %+v ok=%v", loc, ok)
	}
	loc, ok = parseHFResolve("https://huggingface.co/ggml-org/Clef-GGUF/resolve/main/Clef-Q8_0.gguf?download=true")
	if !ok || loc.Folder != "" || loc.File != "Clef-Q8_0.gguf" {
		t.Fatalf("got %+v ok=%v", loc, ok)
	}
	for _, bad := range []string{
		"https://example.com/a/b/resolve/main/x.gguf",
		"https://huggingface.co/a/b/blob/main/x.gguf",
		"https://huggingface.co/a/b/resolve/main/",
		"http://huggingface.co/a/b/resolve/main/x.gguf",
	} {
		if _, ok := parseHFResolve(bad); ok {
			t.Errorf("%s: parsed, want rejected", bad)
		}
	}
}

// fakeHub serves a tree API from a map of folder -> entries, paginating
// the root folder in two pages to exercise the Link header.
func fakeHub(t *testing.T, trees map[string][]hfTreeEntry) *httptest.Server {
	t.Helper()
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		const prefix = "/api/models/acme/Model-GGUF/tree/main"
		if !strings.HasPrefix(r.URL.Path, prefix) {
			http.NotFound(w, r)
			return
		}
		folder := strings.TrimPrefix(strings.TrimPrefix(r.URL.Path, prefix), "/")
		entries, ok := trees[folder]
		if !ok {
			http.Error(w, `{"error":"no such folder"}`, http.StatusNotFound)
			return
		}
		page := r.URL.Query().Get("page")
		if folder == "" && len(entries) > 1 {
			if page == "" {
				w.Header().Set("Link", fmt.Sprintf(`<%s%s?page=2>; rel="next"`, srv.URL, prefix))
				entries = entries[:1]
			} else {
				entries = entries[1:]
			}
		}
		_ = json.NewEncoder(w).Encode(entries)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func lfs(oid string, size int64) *struct {
	OID  string `json:"oid"`
	Size int64  `json:"size"`
} {
	return &struct {
		OID  string `json:"oid"`
		Size int64  `json:"size"`
	}{oid, size}
}

func writeRemoteFixture(t *testing.T, quants []Quant) string {
	t.Helper()
	root := t.TempDir()
	m := validManifest()
	m.Quants = quants
	raw, err := yaml.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "catalog"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "catalog", "acme.yaml"), raw, 0o644); err != nil {
		t.Fatal(err)
	}
	return root
}

func TestVerifyRemoteMatches(t *testing.T) {
	shaA, shaB, shaM := strings.Repeat("aa", 32), strings.Repeat("bb", 32), strings.Repeat("cc", 32)
	hub := fakeHub(t, map[string][]hfTreeEntry{
		"": {
			{Type: "file", Path: "README.md", Size: 12},
			{Type: "file", Path: "Model-Q4_K_M.gguf", Size: 100, LFS: lfs(shaA, 100)},
			{Type: "file", Path: "mmproj-F16.gguf", Size: 7, LFS: lfs(shaM, 7)},
		},
		"Q8_0": {
			{Type: "file", Path: "Q8_0/Model-Q8_0-00001-of-00002.gguf", LFS: lfs(shaB, 60)},
			{Type: "file", Path: "Q8_0/Model-Q8_0-00002-of-00002.gguf", LFS: lfs(shaA, 40)},
		},
	})
	root := writeRemoteFixture(t, []Quant{
		{Quant: "Q4_K_M", ArtifactURL: "https://huggingface.co/acme/Model-GGUF/resolve/main/Model-Q4_K_M.gguf", SHA256: shaA, SizeBytes: 100,
			Mmproj: &Part{URL: "https://huggingface.co/acme/Model-GGUF/resolve/main/mmproj-F16.gguf", SHA256: shaM, SizeBytes: 7}},
		{Quant: "Q8_0", SizeBytes: 100, Parts: []Part{
			{URL: "https://huggingface.co/acme/Model-GGUF/resolve/main/Q8_0/Model-Q8_0-00001-of-00002.gguf", SHA256: shaB, SizeBytes: 60},
			{URL: "https://huggingface.co/acme/Model-GGUF/resolve/main/Q8_0/Model-Q8_0-00002-of-00002.gguf", SHA256: shaA, SizeBytes: 40},
		}},
		{Quant: "Q6_K", ArtifactURL: "https://huggingface.co/acme/Model-GGUF/resolve/main/Model-Q6_K.gguf", SHA256: shaPlaceholder, SizeBytes: 1},
		{Quant: "Q5_K_M", ArtifactURL: "https://mirror.example.com/Model-Q5_K_M.gguf", SHA256: shaA, SizeBytes: 1},
	})
	rep, err := VerifyRemote(root, RemoteOptions{BaseURL: hub.URL})
	if err != nil {
		t.Fatalf("VerifyRemote: %v", err)
	}
	if len(rep.Issues) != 0 {
		t.Fatalf("issues: %v", rep.Issues)
	}
	if rep.Checked != 4 || rep.Folders != 2 || len(rep.Skipped) != 2 {
		t.Fatalf("checked=%d folders=%d skipped=%v", rep.Checked, rep.Folders, rep.Skipped)
	}
}

func TestVerifyRemoteReportsDrift(t *testing.T) {
	shaA, shaB := strings.Repeat("aa", 32), strings.Repeat("bb", 32)
	hub := fakeHub(t, map[string][]hfTreeEntry{
		"": {
			{Type: "file", Path: "Model-Q4_K_M.gguf", LFS: lfs(shaB, 101)}, // re-uploaded
			{Type: "file", Path: "Model-Q3_K_M.gguf", Size: 5},             // not LFS
		},
	})
	root := writeRemoteFixture(t, []Quant{
		{Quant: "Q4_K_M", ArtifactURL: "https://huggingface.co/acme/Model-GGUF/resolve/main/Model-Q4_K_M.gguf", SHA256: shaA, SizeBytes: 100},
		{Quant: "Q3_K_M", ArtifactURL: "https://huggingface.co/acme/Model-GGUF/resolve/main/Model-Q3_K_M.gguf", SHA256: shaA, SizeBytes: 5},
		{Quant: "Q8_0", ArtifactURL: "https://huggingface.co/acme/Model-GGUF/resolve/main/Model-Q8_0.gguf", SHA256: shaA, SizeBytes: 5},
	})
	rep, err := VerifyRemote(root, RemoteOptions{BaseURL: hub.URL})
	if err != nil {
		t.Fatalf("VerifyRemote: %v", err)
	}
	if rep.Checked != 0 {
		t.Errorf("checked=%d, want 0", rep.Checked)
	}
	want := []string{
		"quant Q4_K_M: sha256 drift — manifest " + shaA + ", upstream " + shaB,
		"quant Q4_K_M: size_bytes drift — manifest 100, upstream 101",
		"quant Q3_K_M: Model-Q3_K_M.gguf is not an LFS object",
		"quant Q8_0: Model-Q8_0.gguf no longer exists upstream",
	}
	if len(rep.Issues) != len(want) {
		t.Fatalf("issues: %v", rep.Issues)
	}
	for i, w := range want {
		if !strings.Contains(rep.Issues[i], w) {
			t.Errorf("issue %d = %q, want it to contain %q", i, rep.Issues[i], w)
		}
	}
}

func TestVerifyRemoteFetchFailureIsAnError(t *testing.T) {
	hub := fakeHub(t, map[string][]hfTreeEntry{})
	root := writeRemoteFixture(t, []Quant{
		{Quant: "Q4_K_M", ArtifactURL: "https://huggingface.co/acme/Model-GGUF/resolve/main/Model-Q4_K_M.gguf", SHA256: strings.Repeat("aa", 32), SizeBytes: 1},
	})
	if _, err := VerifyRemote(root, RemoteOptions{BaseURL: hub.URL}); err == nil || !strings.Contains(err.Error(), "HTTP 404") {
		t.Fatalf("err = %v, want an HTTP 404 error", err)
	}
}
