package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Test hashes are synthetic (a repeated hex pair with a letter in it so
// YAML reads them as strings), never real artifact hashes — a fixture must
// not look like a verified pin (CLAUDE.md rule 3).
func fakeSHA(b byte) string { return strings.Repeat(fmt.Sprintf("%02x", b), 32) }

const partsBase = "https://huggingface.co/x/Big-GGUF/resolve/main/Big-Q4_K_M"

// validParts is a well-formed 3-part quant.
func validParts() Quant {
	q := validQuant()
	q.ArtifactURL, q.SHA256 = "", ""
	q.Parts = []Part{
		{URL: partsBase + "/Big-Q4_K_M-00001-of-00003.gguf", SHA256: fakeSHA(0xa1), SizeBytes: 2_000_000_000},
		{URL: partsBase + "/Big-Q4_K_M-00002-of-00003.gguf", SHA256: fakeSHA(0xb2), SizeBytes: 2_000_000_000},
		{URL: partsBase + "/Big-Q4_K_M-00003-of-00003.gguf", SHA256: fakeSHA(0xc3), SizeBytes: 920_739_232},
	}
	q.SizeBytes = 4_920_739_232
	return q
}

func TestValidPartsQuantPasses(t *testing.T) {
	if issues := CheckQuant(validParts()); len(issues) != 0 {
		t.Fatalf("unexpected issues: %v", issues)
	}
	// TODO-verify is allowed per part while in draft.
	q := validParts()
	q.Parts[1].SHA256 = shaPlaceholder
	if issues := CheckQuant(q); len(issues) != 0 {
		t.Fatalf("TODO-verify part rejected: %v", issues)
	}
}

func TestPartsOutOfOrder(t *testing.T) {
	q := validParts()
	q.Parts[0], q.Parts[1] = q.Parts[1], q.Parts[0]
	issues := CheckQuant(q)
	assertIssue(t, issues, "expected shard 00001, got 00002")
	assertIssue(t, issues, "expected shard 00002, got 00001")
}

func TestPartsMissingShard(t *testing.T) {
	// Truncated list: the series says 3, only 2 listed.
	q := validParts()
	q.Parts = q.Parts[:2]
	q.SizeBytes = q.Parts[0].SizeBytes + q.Parts[1].SizeBytes
	assertIssue(t, CheckQuant(q), "series says 3 parts (-of-00003) but 2 are listed")

	// A gap in the middle: 1 and 3 of 3.
	q = validParts()
	q.Parts = []Part{q.Parts[0], q.Parts[2]}
	q.SizeBytes = q.Parts[0].SizeBytes + q.Parts[1].SizeBytes
	issues := CheckQuant(q)
	assertIssue(t, issues, "series says 3 parts")
	assertIssue(t, issues, "expected shard 00002, got 00003")
}

func TestPartsSumMismatch(t *testing.T) {
	q := validParts()
	q.SizeBytes++
	assertIssue(t, CheckQuant(q), "is not the sum of parts")
}

func TestPartsAndArtifactURLAreExclusive(t *testing.T) {
	q := validParts()
	q.ArtifactURL = validQuant().ArtifactURL
	q.SHA256 = validQuant().SHA256
	assertIssue(t, CheckQuant(q), "mutually exclusive")
}

func TestPartsSeriesShape(t *testing.T) {
	// Not a series basename at all.
	q := validParts()
	q.Parts[2].URL = partsBase + "/Big-Q4_K_M.gguf"
	assertIssue(t, CheckQuant(q), "is not of the form <name>-NNNNN-of-NNNNN.gguf")

	// Different prefix / directory / count than parts[0].
	q = validParts()
	q.Parts[1].URL = partsBase + "/Other-Q4_K_M-00002-of-00003.gguf"
	assertIssue(t, CheckQuant(q), "basename prefix")
	q = validParts()
	q.Parts[1].URL = "https://huggingface.co/y/Big-GGUF/resolve/main/Big-Q4_K_M/Big-Q4_K_M-00002-of-00003.gguf"
	assertIssue(t, CheckQuant(q), "not in the same directory")
	q = validParts()
	q.Parts[1].URL = partsBase + "/Big-Q4_K_M-00002-of-00004.gguf"
	assertIssue(t, CheckQuant(q), "series count -of-00004 differs")

	// Each part must name the quant, like artifact_url does.
	q = validParts()
	for i := range q.Parts {
		q.Parts[i].URL = fmt.Sprintf("https://huggingface.co/x/Big-GGUF/resolve/main/Big-Q8_0-%05d-of-00003.gguf", i+1)
	}
	assertIssue(t, CheckQuant(q), "parts[0]: url \"https://huggingface.co/x/Big-GGUF/resolve/main/Big-Q8_0-00001-of-00003.gguf\" does not contain quant name")

	// Bad per-part sha shape.
	q = validParts()
	q.Parts[0].SHA256 = strings.ToUpper(fakeSHA(0xa1))
	assertIssue(t, CheckQuant(q), "parts[0]: sha256")
}

func TestShardOneAloneInArtifactURLRejected(t *testing.T) {
	q := validQuant()
	q.ArtifactURL = partsBase + "/Big-Q4_K_M-00001-of-00003.gguf"
	assertIssue(t, CheckQuant(q), "is one shard of a sharded GGUF")
}

func TestMmprojShape(t *testing.T) {
	good := &Part{URL: "https://huggingface.co/x/Big-GGUF/resolve/main/mmproj-F16.gguf", SHA256: fakeSHA(0xd4), SizeBytes: 800_000_000}
	q := validQuant()
	q.Mmproj = good
	if issues := CheckQuant(q); len(issues) != 0 {
		t.Fatalf("single-file + mmproj: unexpected issues %v", issues)
	}
	q = validParts()
	q.Mmproj = good
	if issues := CheckQuant(q); len(issues) != 0 {
		t.Fatalf("parts + mmproj: unexpected issues %v", issues)
	}

	q = validQuant()
	q.Mmproj = &Part{URL: "https://huggingface.co/x/Big-GGUF/resolve/main/Big-Q4_K_M.gguf", SHA256: fakeSHA(0xd4), SizeBytes: 1}
	assertIssue(t, CheckQuant(q), "mmproj: url basename")
	q.Mmproj = &Part{URL: good.URL, SHA256: "nope", SizeBytes: 1}
	assertIssue(t, CheckQuant(q), "mmproj: sha256")
	q.Mmproj = &Part{URL: good.URL, SHA256: fakeSHA(0xd4)}
	assertIssue(t, CheckQuant(q), "mmproj: size_bytes must be positive")
}

func TestCompositeSHA256IsOrderSensitive(t *testing.T) {
	q := validParts()
	a := CompositeSHA256(q.Parts)
	if !sha256RE.MatchString(a) {
		t.Fatalf("composite %q is not lowercase hex sha256", a)
	}
	q.Parts[0], q.Parts[1] = q.Parts[1], q.Parts[0]
	if b := CompositeSHA256(q.Parts); a == b {
		t.Fatal("composite must depend on part order")
	}
}

// scratchRepo copies schema/ and fingerprints/ from the real repo into a
// temp root and writes the given manifests under catalog/, so Run (schema
// + cross-checks) can be exercised on fixtures.
func scratchRepo(t *testing.T, manifests map[string]string) string {
	t.Helper()
	root := t.TempDir()
	for _, dir := range []string{"schema", "fingerprints/prompts"} {
		src := filepath.Join(repoRoot, dir)
		dst := filepath.Join(root, dir)
		if err := os.MkdirAll(dst, 0o755); err != nil {
			t.Fatal(err)
		}
		entries, err := os.ReadDir(src)
		if err != nil {
			t.Fatal(err)
		}
		for _, e := range entries {
			if e.IsDir() {
				continue
			}
			b, err := os.ReadFile(filepath.Join(src, e.Name()))
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dst, e.Name()), b, 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := os.MkdirAll(filepath.Join(root, "catalog"), 0o755); err != nil {
		t.Fatal(err)
	}
	for name, body := range manifests {
		if err := os.WriteFile(filepath.Join(root, "catalog", name+".yaml"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

// shardedManifest is a schema-complete fixture manifest with a 3-part
// Q4_K_M and an mmproj sidecar. Hashes are synthetic test data.
func shardedManifest(extraQuantLines string) string {
	return fmt.Sprintf(`id: big-vl-70b
display_name: Big VL 70B
family: big
params_b: 70.5
architecture: dense
license:
  name: Apache-2.0
  url: https://example.com/LICENSE
payout_class: large
context_length: 32768
embeddings: false
fingerprint_set_id: fp-gen-v1
base_payout_rate: 0.385
customer_price_per_mtok: 0.70
source_repo: https://huggingface.co/x/Big-GGUF
quants:
  - quant: Q4_K_M
    parts:
      - url: %s/Big-Q4_K_M-00001-of-00003.gguf
        sha256: %s
        size_bytes: 2000000000
      - url: %s/Big-Q4_K_M-00002-of-00003.gguf
        sha256: %s
        size_bytes: 2000000000
      - url: %s/Big-Q4_K_M-00003-of-00003.gguf
        sha256: %s
        size_bytes: 920739232
    mmproj:
      url: https://huggingface.co/x/Big-GGUF/resolve/main/mmproj-F16.gguf
      sha256: %s
      size_bytes: 800000000
    size_bytes: 4920739232
    min_vram_mb: 5800
    min_ram_mb: 8192
    tok_s_estimates:
      rtx-4090: { min: 20, max: 40 }
%s`, partsBase, fakeSHA(0xa1), partsBase, fakeSHA(0xb2), partsBase, fakeSHA(0xc3), fakeSHA(0xd4), extraQuantLines)
}

func TestRunAcceptsShardedManifestAndEmitsParts(t *testing.T) {
	root := scratchRepo(t, map[string]string{"big-vl-70b": shardedManifest("")})
	issues, err := Run(root)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(issues) != 0 {
		t.Fatalf("unexpected issues: %v", issues)
	}

	buf, err := EmitFlat(root)
	if err != nil {
		t.Fatalf("EmitFlat: %v", err)
	}
	var doc struct {
		Models []flatModel `json:"models"`
	}
	if err := json.Unmarshal(buf, &doc); err != nil {
		t.Fatal(err)
	}
	if len(doc.Models) != 1 {
		t.Fatalf("want 1 flat model, got %d", len(doc.Models))
	}
	fm := doc.Models[0]
	if fm.ID != "big-vl-70b-q4_k_m" || len(fm.Parts) != 3 || fm.Mmproj == nil {
		t.Fatalf("flat entry not carrying parts/mmproj: %+v", fm)
	}
	if fm.ArtifactURL != "" {
		t.Errorf("sharded flat entry must have an empty artifact_url, got %q", fm.ArtifactURL)
	}
	if want := CompositeSHA256(fm.Parts); fm.SHA256 != want {
		t.Errorf("flat sha256 = %q, want composite %q", fm.SHA256, want)
	}
	if fm.SizeBytes != 4920739232 {
		t.Errorf("size_bytes = %d, want the summed total", fm.SizeBytes)
	}
	if fm.Parts[2].URL != partsBase+"/Big-Q4_K_M-00003-of-00003.gguf" || fm.Parts[2].SHA256 != fakeSHA(0xc3) {
		t.Errorf("parts not emitted verbatim: %+v", fm.Parts[2])
	}
	if !strings.Contains(string(buf), `"parts": [`) || !strings.Contains(string(buf), `"mmproj": {`) {
		t.Errorf("flat JSON lacks parts/mmproj keys:\n%s", buf)
	}
}

func TestEmitSkipsShardedQuantWithUnverifiedPart(t *testing.T) {
	body := strings.Replace(shardedManifest(""), fakeSHA(0xb2), shaPlaceholder, 1)
	root := scratchRepo(t, map[string]string{"big-vl-70b": body})
	if issues, err := Run(root); err != nil || len(issues) != 0 {
		t.Fatalf("draft manifest must validate: err=%v issues=%v", err, issues)
	}
	if _, err := EmitFlat(root); err == nil || !strings.Contains(err.Error(), "no verifiable quants") {
		t.Fatalf("a quant with a TODO-verify part must not be emitted, got err=%v", err)
	}
}

// The schema itself must reject the mixed shape and the lone shard, so a
// hand-edited manifest fails before the cross-checks run.
func TestSchemaRejectsMixedAndShardOne(t *testing.T) {
	mixed := strings.Replace(shardedManifest(""), "    parts:\n",
		"    artifact_url: "+partsBase+".gguf\n    sha256: "+fakeSHA(0xe5)+"\n    parts:\n", 1)
	root := scratchRepo(t, map[string]string{"big-vl-70b": mixed})
	issues, err := Run(root)
	if err != nil {
		t.Fatal(err)
	}
	assertIssue(t, issues, "schema")

	onlyURL := shardedManifest("")
	onlyURL = onlyURL[:strings.Index(onlyURL, "    parts:\n")] +
		"    artifact_url: " + partsBase + "/Big-Q4_K_M-00001-of-00003.gguf\n    sha256: " + fakeSHA(0xe5) + "\n" +
		onlyURL[strings.Index(onlyURL, "    size_bytes: 4920739232\n"):]
	root = scratchRepo(t, map[string]string{"big-vl-70b": onlyURL})
	issues, err = Run(root)
	if err != nil {
		t.Fatal(err)
	}
	assertIssue(t, issues, "is one shard of a sharded GGUF")
}
