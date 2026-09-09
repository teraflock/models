package main

import (
	"strings"
	"testing"
)

// Release mode is the lock on catalog/catalog.json: a TODO-verify that
// branch validation tolerates (CLAUDE.md rule 3 — never invent a hash)
// must fail RunWith and EmitFlatWith once Release is set.

// singleFileManifest is the sharded fixture rewritten as a single-file
// quant so the single-file sha256 path is exercised too.
func singleFileManifest(sha string) string {
	body := shardedManifest("")
	start := strings.Index(body, "    parts:\n")
	end := strings.Index(body, "    size_bytes: 4920739232\n")
	return body[:start] +
		"    artifact_url: " + partsBase + ".gguf\n    sha256: " + sha + "\n" +
		body[end:]
}

func TestReleaseAcceptsFullyPinnedCatalog(t *testing.T) {
	root := scratchRepo(t, map[string]string{
		"big-vl-70b": singleFileManifest(fakeSHA(0xe5)),
	})
	issues, err := RunWith(root, Options{Release: true})
	if err != nil {
		t.Fatalf("RunWith: %v", err)
	}
	if len(issues) != 0 {
		t.Fatalf("pinned catalog must pass release mode, got %v", issues)
	}
	if _, err := EmitFlatWith(root, Options{Release: true}); err != nil {
		t.Fatalf("EmitFlatWith: %v", err)
	}
}

func TestReleaseRejectsSingleFilePlaceholder(t *testing.T) {
	root := scratchRepo(t, map[string]string{
		"big-vl-70b": singleFileManifest(shaPlaceholder),
	})
	if issues, err := Run(root); err != nil || len(issues) != 0 {
		t.Fatalf("branch mode must accept the placeholder: err=%v issues=%v", err, issues)
	}
	issues, err := RunWith(root, Options{Release: true})
	if err != nil {
		t.Fatalf("RunWith: %v", err)
	}
	assertIssue(t, issues, "release: quant Q4_K_M: sha256 is \"TODO-verify\"")
	if len(issues) != 1 {
		t.Errorf("want exactly one release issue, got %v", issues)
	}
	if _, err := EmitFlatWith(root, Options{Release: true}); err == nil || !strings.Contains(err.Error(), "release catalog cannot carry") {
		t.Fatalf("release emit must refuse a placeholder, got err=%v", err)
	}
}

func TestReleaseRejectsPlaceholderPartAndMmproj(t *testing.T) {
	body := strings.Replace(shardedManifest(""), fakeSHA(0xb2), shaPlaceholder, 1) // part 2
	body = strings.Replace(body, fakeSHA(0xd4), shaPlaceholder, 1)                 // mmproj
	root := scratchRepo(t, map[string]string{"big-vl-70b": body})
	if issues, err := Run(root); err != nil || len(issues) != 0 {
		t.Fatalf("branch mode must accept the placeholders: err=%v issues=%v", err, issues)
	}
	issues, err := RunWith(root, Options{Release: true})
	if err != nil {
		t.Fatalf("RunWith: %v", err)
	}
	assertIssue(t, issues, "parts[1] (Big-Q4_K_M-00002-of-00003.gguf) sha256 is \"TODO-verify\"")
	assertIssue(t, issues, "mmproj sha256 is \"TODO-verify\"")
	if len(issues) != 2 {
		t.Errorf("want exactly two release issues, got %v", issues)
	}
	if _, err := EmitFlatWith(root, Options{Release: true}); err == nil {
		t.Fatal("release emit must refuse a placeholder part")
	}
}

// A release must not shrink silently: a catalog whose only quant is
// unverified fails release emit with the placeholder named, whereas branch
// emit reports merely "no verifiable quants".
func TestReleaseEmitNamesTheOffendingQuant(t *testing.T) {
	root := scratchRepo(t, map[string]string{
		"big-vl-70b": singleFileManifest(shaPlaceholder),
	})
	_, err := EmitFlatWith(root, Options{Release: true})
	if err == nil || !strings.Contains(err.Error(), "big-vl-70b.yaml: quant Q4_K_M") {
		t.Fatalf("want the offending file and quant in the error, got %v", err)
	}
}
