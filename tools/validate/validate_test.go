package main

import (
	"strings"
	"testing"
)

// repoRoot is relative to this package directory (tools/validate).
const repoRoot = "../.."

// TestRepoIsValid is the integration gate: the committed catalog and
// fingerprint sets must validate cleanly.
func TestRepoIsValid(t *testing.T) {
	issues, err := Run(repoRoot)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	for _, is := range issues {
		t.Errorf("issue: %s", is)
	}
}

func validManifest() Manifest {
	return Manifest{
		ID:            "llama-3.1-8b-instruct",
		Family:        "llama-3.1",
		ParamsB:       8.03,
		License:       License{Name: "Llama 3.1 Community License", URL: "https://example.com"},
		PayoutClass:   "small",
		ContextLength: 131072,
		FingerprintID: "fp-gen-v1",
		BasePayout:    0.055,
		CustomerPrice: 0.10,
		SourceRepo:    "https://huggingface.co/bartowski/Meta-Llama-3.1-8B-Instruct-GGUF",
		Quants:        []Quant{validQuant()},
	}
}

func validQuant() Quant {
	return Quant{
		Quant:       "Q4_K_M",
		ArtifactURL: "https://huggingface.co/x/resolve/main/Meta-Llama-3.1-8B-Instruct-Q4_K_M.gguf",
		SHA256:      strings.Repeat("ab", 32),
		SizeBytes:   4920739232,
		MinVRAMMB:   5800,
		MinRAMMB:    8192,
		TokS:        map[string]Envelope{"rtx-4090": {Min: 90, Max: 140}},
	}
}

var testSets = map[string]FingerprintSet{
	"fp-gen-v1":   {ID: "fp-gen-v1", Kind: "generation"},
	"fp-embed-v1": {ID: "fp-embed-v1", Kind: "embedding"},
}

func assertIssue(t *testing.T, issues []string, substr string) {
	t.Helper()
	for _, is := range issues {
		if strings.Contains(is, substr) {
			return
		}
	}
	t.Errorf("expected an issue containing %q, got %v", substr, issues)
}

func TestValidManifestPasses(t *testing.T) {
	if issues := CheckManifest(validManifest(), testSets); len(issues) != 0 {
		t.Fatalf("unexpected issues: %v", issues)
	}
}

func TestPayoutClassVsParams(t *testing.T) {
	cases := []struct {
		class   string
		paramsB float64
		ok      bool
	}{
		{"nano", 1.54, true},
		{"nano", 3.21, true},
		{"nano", 8.03, false},
		{"small", 7.62, true},
		{"small", 3.21, false},
		{"mid", 32.76, true},
		{"mid", 70.55, false},
		{"large", 70.55, true},
		{"large", 8.03, false},
	}
	for _, c := range cases {
		m := validManifest()
		m.PayoutClass = c.class
		m.ParamsB = c.paramsB
		p := classPricing[c.class]
		m.BasePayout, m.CustomerPrice = p[0], p[1]
		issues := CheckManifest(m, testSets)
		var mismatch bool
		for _, is := range issues {
			if strings.Contains(is, "does not fit params_b") {
				mismatch = true
			}
		}
		if c.ok && mismatch {
			t.Errorf("class=%s params_b=%g: unexpected class/params issue: %v", c.class, c.paramsB, issues)
		}
		if !c.ok && !mismatch {
			t.Errorf("class=%s params_b=%g: expected class/params issue, got %v", c.class, c.paramsB, issues)
		}
	}
}

func TestPricingTable(t *testing.T) {
	m := validManifest()
	m.CustomerPrice = 0.25 // not the §7 small price
	assertIssue(t, CheckManifest(m, testSets), "does not match §7 table")

	m = validManifest()
	m.BasePayout = 0.20 // above customer price and off-table
	issues := CheckManifest(m, testSets)
	assertIssue(t, issues, "does not match §7 table")
	assertIssue(t, issues, "must be below customer_price_per_mtok")
}

func TestEmbeddingsPricingOverride(t *testing.T) {
	m := validManifest()
	m.ID = "nomic-embed-text-v1.5"
	m.ParamsB = 0.137
	m.PayoutClass = "nano"
	m.Embeddings = true
	m.FingerprintID = "fp-embed-v1"
	m.BasePayout, m.CustomerPrice = 0.0055, 0.01
	if issues := CheckManifest(m, testSets); len(issues) != 0 {
		t.Fatalf("unexpected issues: %v", issues)
	}
	// Class-table pricing on an embeddings model must fail.
	m.BasePayout, m.CustomerPrice = 0.022, 0.04
	assertIssue(t, CheckManifest(m, testSets), "does not match §7 table")
}

func TestFingerprintReference(t *testing.T) {
	m := validManifest()
	m.FingerprintID = "fp-does-not-exist"
	assertIssue(t, CheckManifest(m, testSets), "has no file in fingerprints/prompts/")

	// Kind mismatch: chat model pointing at an embedding set.
	m = validManifest()
	m.FingerprintID = "fp-embed-v1"
	assertIssue(t, CheckManifest(m, testSets), `has kind "embedding", model requires "generation"`)
}

func TestQuantNaming(t *testing.T) {
	for _, good := range []string{"Q4_K_M", "Q8_0", "Q5_K_S", "IQ2_XXS", "F16", "BF16"} {
		q := validQuant()
		q.Quant = good
		q.ArtifactURL = "https://x/m-" + good + ".gguf"
		for _, is := range CheckQuant(q) {
			if strings.Contains(is, "not a canonical") {
				t.Errorf("%s flagged as non-canonical", good)
			}
		}
	}
	for _, bad := range []string{"q4_k_m", "Q4KM", "Q9_0", "INT8", "GPTQ"} {
		q := validQuant()
		q.Quant = bad
		assertIssue(t, CheckQuant(q), "not a canonical")
	}
}

func TestQuantURLMustMentionQuant(t *testing.T) {
	q := validQuant()
	q.ArtifactURL = "https://huggingface.co/x/resolve/main/model-Q8_0.gguf" // says Q8_0, quant is Q4_K_M
	assertIssue(t, CheckQuant(q), "does not contain quant name")

	// Lower-case file names (e.g. nomic's .f16.gguf) must be accepted.
	q = validQuant()
	q.Quant = "F16"
	q.ArtifactURL = "https://huggingface.co/x/resolve/main/nomic-embed-text-v1.5.f16.gguf"
	for _, is := range CheckQuant(q) {
		if strings.Contains(is, "does not contain quant name") {
			t.Errorf("case-insensitive match failed: %s", is)
		}
	}
}

func TestVRAMSanity(t *testing.T) {
	q := validQuant()
	q.MinVRAMMB = 1024 // artifact is ~4693 MB
	assertIssue(t, CheckQuant(q), "below the artifact size")

	q = validQuant()
	q.MinVRAMMB = 50000 // >4x artifact size
	assertIssue(t, CheckQuant(q), "more than 4x")

	q = validQuant()
	q.MinRAMMB = 512
	assertIssue(t, CheckQuant(q), "min_ram_mb 512 is below")
}

func TestSHAPlaceholderAllowedButFakeHexRejected(t *testing.T) {
	q := validQuant()
	q.SHA256 = "TODO-verify"
	for _, is := range CheckQuant(q) {
		if strings.Contains(is, "sha256") {
			t.Errorf("TODO-verify should be accepted: %s", is)
		}
	}
	q.SHA256 = "not-a-hash"
	assertIssue(t, CheckQuant(q), "sha256")
	q.SHA256 = strings.ToUpper(strings.Repeat("ab", 32)) // uppercase hex rejected
	assertIssue(t, CheckQuant(q), "sha256")
}

func TestFingerprintSetChecks(t *testing.T) {
	s := FingerprintSet{
		ID:   "fp-gen-x",
		Kind: "generation",
		Defaults: &FPDefaults{
			Temperature: 0.7, // non-greedy: not deterministic
			Seed:        42,
			MaxTokens:   32,
		},
		Prompts: []FPPrompt{
			{ID: "a", Category: "arithmetic-chain", Prompt: "2+2?"},
			{ID: "a", Category: "rare-token", Prompt: "continue"}, // dup id
			{ID: "b", Category: "format-following", Input: "wrong field"},
		},
	}
	issues := CheckFingerprintSet(s)
	assertIssue(t, issues, "temperature 0")
	assertIssue(t, issues, `duplicate prompt id "a"`)
	assertIssue(t, issues, "need a non-empty 'prompt'")
	assertIssue(t, issues, "must not set 'input'")
}
