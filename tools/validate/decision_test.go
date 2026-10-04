package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// Decision models (models#6; proto design note 2026-10-03-decision-models):
// `decision: true` is exclusive with `embeddings: true`, carries the class
// price row unchanged, and must point at a kind: decision fingerprint set.

var decisionSets = map[string]FingerprintSet{
	"fp-gen-v1":      {ID: "fp-gen-v1", Kind: "generation"},
	"fp-embed-v1":    {ID: "fp-embed-v1", Kind: "embedding"},
	"fp-decision-v1": {ID: "fp-decision-v1", Kind: "decision"},
}

func decisionManifest() Manifest {
	m := validManifest()
	m.ID = "kev-4b"
	m.ParamsB = 4.2
	m.Decision = true
	m.FingerprintID = "fp-decision-v1"
	return m
}

func TestDecisionManifestPassesAtClassPricing(t *testing.T) {
	if issues := CheckManifest(decisionManifest(), decisionSets); len(issues) != 0 {
		t.Fatalf("unexpected issues: %v", issues)
	}
	// Every class: the row is copied as it stands, output price included.
	for class, params := range map[string]float64{"nano": 0.421, "small": 9, "mid": 27} {
		m := decisionManifest()
		m.PayoutClass, m.ParamsB = class, params
		priceAs(&m, class)
		if issues := CheckManifest(m, decisionSets); len(issues) != 0 {
			t.Errorf("%s: unexpected issues: %v", class, issues)
		}
	}
}

// SPEC §7 rejected a flat decision row: neither the embeddings row nor
// any off-table value is accepted on a decision model.
func TestDecisionHasNoPricingOverride(t *testing.T) {
	m := decisionManifest()
	m.PriceIn, m.PriceOut, m.PayoutShare = embeddingPricing.In, embeddingPricing.Out, embeddingPricing.Share
	assertIssue(t, CheckManifest(m, decisionSets), "does not match §7 table")

	m = decisionManifest()
	m.PriceOut = 0.001 // "no output tokens" is not a licence to zero the column
	assertIssue(t, CheckManifest(m, decisionSets), "does not match §7 table")
}

func TestDecisionAndEmbeddingsAreExclusive(t *testing.T) {
	m := decisionManifest()
	m.Embeddings = true
	assertIssue(t, CheckManifest(m, decisionSets), "mutually exclusive")
}

func TestDecisionFingerprintKind(t *testing.T) {
	// A decision model on a generation or embedding set.
	for _, set := range []string{"fp-gen-v1", "fp-embed-v1"} {
		m := decisionManifest()
		m.FingerprintID = set
		assertIssue(t, CheckManifest(m, decisionSets), `model requires "decision"`)
	}
	// A chat model on the decision set.
	m := validManifest()
	m.FingerprintID = "fp-decision-v1"
	assertIssue(t, CheckManifest(m, decisionSets), `has kind "decision", model requires "generation"`)
}

// The small class is (3.5, 9]: a 9B model (Clef-flash) is small, anything
// above is mid.
func TestSmallClassIncludesNineB(t *testing.T) {
	m := decisionManifest()
	m.ParamsB = 9
	if issues := CheckManifest(m, decisionSets); len(issues) != 0 {
		t.Fatalf("9B must class as small: %v", issues)
	}
	m.ParamsB = 9.01
	assertIssue(t, CheckManifest(m, decisionSets), "does not fit params_b")
}

// The reference quant name the ggml-org decision conversions ship.
func TestBF16IsACanonicalQuantName(t *testing.T) {
	q := validQuant()
	q.Quant = "BF16"
	q.ArtifactURL = "https://huggingface.co/ggml-org/Laya-GGUF/resolve/main/Laya-BF16.gguf"
	if issues := CheckQuant(q); len(issues) != 0 {
		t.Fatalf("unexpected issues: %v", issues)
	}
}

// parseSet decodes a fingerprint set the way RunWith does.
func parseSet(t *testing.T, body string) FingerprintSet {
	t.Helper()
	var s FingerprintSet
	if err := yaml.Unmarshal([]byte(body), &s); err != nil {
		t.Fatalf("yaml: %v", err)
	}
	return s
}

const decisionSetHead = "id: fp-decision-x\nkind: decision\ndescription: fixture\nprompts:\n"

func TestDecisionProbeShapes(t *testing.T) {
	s := parseSet(t, decisionSetHead+`
  - id: ok-string-state
    category: decision-probe
    state: Payouts have failed for three days.
    questions:
      department:
        type: choice
        instructions: "Which team?"
        criteria: { billing: "Payments, refunds", technical: null }
      urgency:
        type: score
        instructions: "How urgent?"
        criteria: [can wait, today, right now]
      escalate:
        type: noul
        instructions: "Human within the hour?"
  - id: ok-json-state
    category: decision-probe
    state: { invoice: { total: 1250, paid: false } }
    questions:
      large:
        type: noul
        instructions: { ask: "Is the total above 1000?" }
        criteria: { "true": above, "false": not above }
`)
	if issues := CheckFingerprintSet(s); len(issues) != 0 {
		t.Fatalf("unexpected issues: %v", issues)
	}
}

func TestDecisionProbeRejections(t *testing.T) {
	manyOptions := "{ "
	for i := 0; i < decisionMaxProbeOptions+1; i++ {
		manyOptions += "o" + string(rune('a'+i)) + ": null, "
	}
	manyOptions += "}"

	cases := []struct {
		name, probe, want string
	}{
		{"no state", `
    questions: { q: { type: noul, instructions: "Yes?" } }`, "need a 'state'"},
		{"empty state", `
    state: ""
    questions: { q: { type: noul, instructions: "Yes?" } }`, "state must be a non-empty string"},
		{"numeric state", `
    state: 42
    questions: { q: { type: noul, instructions: "Yes?" } }`, "state must be a non-empty string"},
		{"no questions", `
    state: s`, "need 'questions'"},
		{"unknown type", `
    state: s
    questions: { q: { type: rank, instructions: Order these } }`, `type "rank" must be choice, score or noul`},
		{"missing instructions", `
    state: s
    questions: { q: { type: noul } }`, "instructions must be a non-empty"},
		{"choice without criteria", `
    state: s
    questions: { q: { type: choice, instructions: "Which?" } }`, "choice needs criteria"},
		{"choice with one option", `
    state: s
    questions: { q: { type: choice, instructions: "Which?", criteria: { only: null } } }`, "choice has 1 options"},
		{"choice over the probe cap", `
    state: s
    questions: { q: { type: choice, instructions: "Which?", criteria: ` + manyOptions + ` } }`, "choice has 21 options"},
		{"score as mapping", `
    state: s
    questions: { q: { type: score, instructions: "How much?", criteria: { low: a, high: b } } }`, "score needs criteria: a list"},
		{"score with one level", `
    state: s
    questions: { q: { type: score, instructions: "How much?", criteria: [only] } }`, "score has 1 levels"},
		{"score with a null level", `
    state: s
    questions: { q: { type: score, instructions: "How much?", criteria: [low, null] } }`, "criteria[1]: level description"},
		{"noul with unquoted boolean keys", `
    state: s
    questions: { q: { type: noul, instructions: "Yes?", criteria: { true: yes it is, false: no it is not } } }`, `must be the quoted string "true" or "false"`},
		{"noul with only true", `
    state: s
    questions: { q: { type: noul, instructions: "Yes?", criteria: { "true": yes it is } } }`, `exactly a "true" and a "false"`},
		{"expected answer smuggled in", `
    state: s
    questions: { q: { type: noul, instructions: "Yes?", noul: 0.63 } }`, "never expected answers"},
		{"generation fields on a probe", `
    state: s
    prompt: 2+2?
    questions: { q: { type: noul, instructions: "Yes?" } }`, "carry only 'state' and 'questions'"},
	}
	for _, c := range cases {
		s := parseSet(t, decisionSetHead+"  - id: p\n    category: decision-probe"+c.probe+"\n")
		issues := CheckFingerprintSet(s)
		found := false
		for _, is := range issues {
			if strings.Contains(is, c.want) {
				found = true
			}
		}
		if !found {
			t.Errorf("%s: expected an issue containing %q, got %v", c.name, c.want, issues)
		}
	}
}

func TestDecisionSetRejectsDefaultsAndForeignKinds(t *testing.T) {
	s := parseSet(t, `id: fp-decision-x
kind: decision
description: fixture
defaults: { temperature: 0, seed: 1, max_tokens: 8 }
prompts:
  - id: p
    category: decision-probe
    state: s
    questions: { q: { type: noul, instructions: "Yes?" } }
`)
	assertIssue(t, CheckFingerprintSet(s), "decision set must not set defaults")

	// state/questions on a generation prompt is a mislabelled set.
	s = parseSet(t, `id: fp-gen-x
kind: generation
description: fixture
defaults: { temperature: 0, seed: 1, max_tokens: 8 }
prompts:
  - id: p
    category: decision-probe
    prompt: hello
    state: s
    questions: { q: { type: noul, instructions: "Yes?" } }
`)
	assertIssue(t, CheckFingerprintSet(s), "must not set 'state'/'questions'")
}

// Question and option order is part of the public contract; the decoded
// probe must keep the order written in the file.
func TestDecisionProbeKeepsDocumentOrder(t *testing.T) {
	s := parseSet(t, decisionSetHead+`
  - id: p
    category: decision-probe
    state: s
    questions:
      zeta: { type: noul, instructions: "Z?" }
      alpha: { type: choice, instructions: "A?", criteria: { second: null, first: null } }
      mid: { type: noul, instructions: "M?" }
`)
	qs := s.Prompts[0].Questions
	var got []string
	for i := 0; i < len(qs.Content); i += 2 {
		got = append(got, qs.Content[i].Value)
	}
	if strings.Join(got, ",") != "zeta,alpha,mid" {
		t.Fatalf("question order = %v, want file order", got)
	}
}

// decisionFixture is a schema-complete decision manifest. Hashes are
// synthetic test data.
func decisionFixture(extraTopLevel string) string {
	return `id: tiny-decider
display_name: Tiny Decider
family: tiny
params_b: 0.4
architecture: dense
license:
  name: Apache-2.0
  url: https://example.com/LICENSE
payout_class: nano
context_length: 512
embeddings: false
` + extraTopLevel + `fingerprint_set_id: fp-decision-v1
price_in_per_mtok: 0.014
price_out_per_mtok: 0.030
payout_share: 0.40
source_repo: https://huggingface.co/x/Tiny-GGUF
quants:
  - quant: BF16
    artifact_url: https://huggingface.co/x/Tiny-GGUF/resolve/main/Tiny-BF16.gguf
    sha256: ` + fakeSHA(0x5d) + `
    size_bytes: 844026720
    min_vram_mb: 1280
    min_ram_mb: 2048
    tok_s_estimates:
      rtx-4090: { min: 1800, max: 4500 }
`
}

// End to end through the schemas: the manifest validates against the real
// fp-decision-v1 set, and the flat catalog carries `decision` on every
// entry — true for the decision model, false (not absent) for the rest.
func TestRunAcceptsDecisionManifestAndEmitsFlag(t *testing.T) {
	root := scratchRepo(t, map[string]string{
		"tiny-decider": decisionFixture("decision: true\n"),
		"big-vl-70b":   singleFileManifest(fakeSHA(0xe5)),
	})
	issues, err := Run(root)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(issues) != 0 {
		t.Fatalf("unexpected issues: %v", issues)
	}
	raw, err := EmitFlat(root)
	if err != nil {
		t.Fatalf("EmitFlat: %v", err)
	}
	var doc struct {
		Models []map[string]any `json:"models"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	got := map[string]any{}
	for _, m := range doc.Models {
		v, present := m["decision"]
		if !present {
			t.Errorf("%v: flat entry has no decision field", m["id"])
		}
		got[m["id"].(string)] = v
	}
	if got["tiny-decider-bf16"] != true {
		t.Errorf("tiny-decider-bf16 decision = %v, want true", got["tiny-decider-bf16"])
	}
	if got["big-vl-70b-q4_k_m"] != false {
		t.Errorf("big-vl-70b-q4_k_m decision = %v, want false", got["big-vl-70b-q4_k_m"])
	}
}

// `decision` is optional (existing manifests omit it) and the schema itself
// refuses the embeddings+decision combination.
func TestSchemaDecisionRules(t *testing.T) {
	// Omitted => a chat model, which may not use the decision set.
	root := scratchRepo(t, map[string]string{"tiny-decider": decisionFixture("")})
	issues, err := Run(root)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	assertIssue(t, issues, `has kind "decision", model requires "generation"`)

	both := strings.Replace(decisionFixture("decision: true\n"), "embeddings: false", "embeddings: true", 1)
	root = scratchRepo(t, map[string]string{"tiny-decider": both})
	issues, err = Run(root)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	assertIssue(t, issues, "schema:")
}

// The schema is the first gate on a probe set: unquoted noul keys, a
// foreign field on a question, and a set with expected outputs must all
// fail before the Go checks run.
func TestSchemaRejectsMalformedDecisionSets(t *testing.T) {
	probe := func(body string) string {
		var b strings.Builder
		b.WriteString("id: fp-decision-bad\nkind: decision\ndescription: fixture\nprompts:\n")
		for i := 0; i < 8; i++ { // schema minItems
			b.WriteString("  - id: p" + string(rune('0'+i)) + "\n    category: decision-probe\n" + body)
		}
		return b.String()
	}
	cases := map[string]string{
		"unquoted noul keys": probe("    state: s\n    questions: { q: { type: noul, instructions: 'Yes?', criteria: { true: a, false: b } } }\n"),
		"expected output":    probe("    state: s\n    questions: { q: { type: noul, instructions: 'Yes?' } }\n    expected: { q: 0.63 }\n"),
		"answer on question": probe("    state: s\n    questions: { q: { type: noul, instructions: 'Yes?', noul: 0.63 } }\n"),
		"missing state":      probe("    questions: { q: { type: noul, instructions: 'Yes?' } }\n"),
		"input instead":      probe("    input: just text\n"),
		"11 score levels":    probe("    state: s\n    questions: { q: { type: score, instructions: 'How?', criteria: [a, b, c, d, e, f, g, h, i, j, k] } }\n"),
	}
	for name, body := range cases {
		root := scratchRepo(t, map[string]string{"tiny-decider": decisionFixture("decision: true\n")})
		path := filepath.Join(root, "fingerprints", "prompts", "fp-decision-bad.yaml")
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		issues, err := Run(root)
		if err != nil {
			t.Fatalf("%s: Run: %v", name, err)
		}
		found := false
		for _, is := range issues {
			if strings.Contains(is, "fp-decision-bad.yaml") {
				found = true
			}
		}
		if !found {
			t.Errorf("%s: expected fp-decision-bad.yaml to be rejected, got %v", name, issues)
		}
	}
}
