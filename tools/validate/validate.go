package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/santhosh-tekuri/jsonschema/v6"
	"gopkg.in/yaml.v3"
)

// Manifest mirrors schema/manifest.schema.json for cross-checks.
type Manifest struct {
	ID            string  `yaml:"id"`
	DisplayName   string  `yaml:"display_name"`
	Family        string  `yaml:"family"`
	ParamsB       float64 `yaml:"params_b"`
	ActiveParamsB float64 `yaml:"active_params_b"`
	Architecture  string  `yaml:"architecture"` // dense|moe|"" (see moeNameRE)
	License       License `yaml:"license"`
	PayoutClass   string  `yaml:"payout_class"`
	ContextLength int     `yaml:"context_length"`
	Embeddings    bool    `yaml:"embeddings"`
	FingerprintID string  `yaml:"fingerprint_set_id"`
	BasePayout    float64 `yaml:"base_payout_rate"`
	CustomerPrice float64 `yaml:"customer_price_per_mtok"`
	SourceRepo    string  `yaml:"source_repo"`
	Quants        []Quant `yaml:"quants"`
}

type License struct {
	Name  string `yaml:"name"`
	URL   string `yaml:"url"`
	Notes string `yaml:"notes"`
}

type Quant struct {
	Quant       string `yaml:"quant"`
	ArtifactURL string `yaml:"artifact_url"` // single-file quants only
	SHA256      string `yaml:"sha256"`       // single-file quants only
	// SizeBytes is the total download: the single file or the sum of Parts
	// (mmproj excluded). Readers that predate Parts keep budgeting on it.
	SizeBytes int64 `yaml:"size_bytes"`
	// Parts is the sharded alternative to ArtifactURL/SHA256: every
	// -NNNNN-of-NNNNN shard, in series order, each pinned.
	Parts []Part `yaml:"parts"`
	// Mmproj is the optional vision projector sidecar (llama-server
	// --mmproj), pinned like a part, not counted in SizeBytes.
	Mmproj    *Part               `yaml:"mmproj"`
	MinVRAMMB int64               `yaml:"min_vram_mb"`
	MinRAMMB  int64               `yaml:"min_ram_mb"`
	TokS      map[string]Envelope `yaml:"tok_s_estimates"`
	Notes     string              `yaml:"notes"`
}

// Part is one pinned file of a multi-file artifact. Field names match
// flock.types.v1.ArtifactPart so nothing is renamed between YAML, the
// flat JSON and the wire.
type Part struct {
	URL       string `yaml:"url" json:"url"`
	SHA256    string `yaml:"sha256" json:"sha256"`
	SizeBytes int64  `yaml:"size_bytes" json:"size_bytes"`
}

type Envelope struct {
	Min float64 `yaml:"min"`
	Max float64 `yaml:"max"`
}

// FingerprintSet mirrors schema/fingerprint-prompts.schema.json.
type FingerprintSet struct {
	ID          string      `yaml:"id"`
	Kind        string      `yaml:"kind"`
	Description string      `yaml:"description"`
	Defaults    *FPDefaults `yaml:"defaults"`
	Prompts     []FPPrompt  `yaml:"prompts"`
}

type FPDefaults struct {
	Temperature float64 `yaml:"temperature"`
	TopP        float64 `yaml:"top_p"`
	Seed        int     `yaml:"seed"`
	MaxTokens   int     `yaml:"max_tokens"`
}

type FPPrompt struct {
	ID        string `yaml:"id"`
	Category  string `yaml:"category"`
	Prompt    string `yaml:"prompt"`
	Input     string `yaml:"input"`
	MaxTokens int    `yaml:"max_tokens"`
}

// classPricing is the SPEC §7 table: payout_class -> (base_payout_rate,
// customer_price_per_mtok) in USD per million tokens.
var classPricing = map[string][2]float64{
	"nano":  {0.022, 0.04},
	"small": {0.055, 0.10},
	"mid":   {0.165, 0.30},
	"large": {0.385, 0.70},
	"xl":    {1.10, 2.00}, // added with SPEC §7 xl row, 2026-08-30
}

// embeddingPricing overrides the class table for embeddings-flagged models
// ("embeddings priced separately (~$0.01)", SPEC §7).
var embeddingPricing = [2]float64{0.0055, 0.01}

// classParamBounds validates payout_class against params_b (billions):
// nano ≤3.5, small (3.5,9], mid (9,40], large (40,150], xl >150.
// Bounds are on TOTAL params — MoE models class (and price) by total, not
// active: what the customer buys is the big model's quality, and what the
// operator must provision is the big model's memory.
var classParamBounds = map[string][2]float64{
	"nano":  {0, 3.5},
	"small": {3.5, 9},
	"mid":   {9, 40},
	"large": {40, 150},
	"xl":    {150, math.MaxFloat64},
}

var (
	// MXFP4 is gpt-oss's native (and only meaningful) quantization.
	quantNameRE = regexp.MustCompile(`^(Q[2-8]_(0|1|K_S|K_M|K_L)|IQ[1-4]_(XXS|XS|S|M|NL)|MXFP4|F16|BF16|F32)$`)
	sha256RE    = regexp.MustCompile(`^[0-9a-f]{64}$`)
)

const shaPlaceholder = "TODO-verify"

// seriesRE matches the basename of one shard of a llama.cpp sharded GGUF:
// <prefix>-<index>-of-<count>.gguf with five-digit zero-padded numbers.
// llama.cpp derives the sibling names from exactly this pattern, so the
// catalog must preserve it verbatim.
var seriesRE = regexp.MustCompile(`^(.+)-(\d{5})-of-(\d{5})\.gguf$`)

// mmprojRE matches the basename of a vision projector sidecar.
var mmprojRE = regexp.MustCompile(`^mmproj.*\.gguf$`)

// CompositeSHA256 is the single "quant sha" of a multi-part artifact: the
// hex sha256 over the concatenation of the lowercase-hex part hashes in
// series order, with no separators. The proto design note
// (proto/docs/design-notes/2026-09-08-sharded-artifacts.md) fixes this
// definition; the control-plane registry and flockd compute the same value
// so DispatchRequest.quant_sha256 pinning keeps meaning for sharded
// models. It is derived, never hand-written into a manifest, and is only
// meaningful when every part hash is real (no TODO-verify).
func CompositeSHA256(parts []Part) string {
	h := sha256.New()
	for _, p := range parts {
		h.Write([]byte(p.SHA256))
	}
	return hex.EncodeToString(h.Sum(nil))
}

// moeNameRE is the naming convention every Mixture-of-Experts release in
// the catalog follows: a "-a<N>b" active-parameter suffix (30b-a3b,
// 80b-a3b, 26b-a4b, 35b-a3b). gpt-oss-style names carry no such hint,
// which is what the explicit `architecture: moe` field is for.
var moeNameRE = regexp.MustCompile(`(?i)-a\d+(\.\d+)?b\b`)

// looksMoE reports whether the id or upstream repo name signals a
// Mixture-of-Experts model.
func looksMoE(m Manifest) bool {
	for _, s := range []string{m.ID, m.SourceRepo} {
		if moeNameRE.MatchString(s) || strings.Contains(strings.ToLower(s), "moe") {
			return true
		}
	}
	return false
}

// Run validates the whole repo rooted at root and returns human-readable
// issues. A non-nil error means the repo could not be validated at all
// (missing schema, unreadable dirs); issues mean the content is wrong.
func Run(root string) ([]string, error) {
	var issues []string

	manifestSchema, err := compileSchema(filepath.Join(root, "schema", "manifest.schema.json"))
	if err != nil {
		return nil, fmt.Errorf("compile manifest schema: %w", err)
	}
	fpSchema, err := compileSchema(filepath.Join(root, "schema", "fingerprint-prompts.schema.json"))
	if err != nil {
		return nil, fmt.Errorf("compile fingerprint schema: %w", err)
	}

	// Fingerprint sets first: manifests reference them.
	sets := map[string]FingerprintSet{}
	fpDir := filepath.Join(root, "fingerprints", "prompts")
	fpFiles, err := filepath.Glob(filepath.Join(fpDir, "*.yaml"))
	if err != nil {
		return nil, err
	}
	if len(fpFiles) == 0 {
		issues = append(issues, fmt.Sprintf("%s: no fingerprint prompt sets found", fpDir))
	}
	for _, path := range fpFiles {
		raw, generic, err := loadYAML(path)
		if err != nil {
			issues = append(issues, fmt.Sprintf("%s: %v", path, err))
			continue
		}
		if err := fpSchema.Validate(generic); err != nil {
			issues = append(issues, fmt.Sprintf("%s: schema: %v", path, err))
			continue
		}
		var set FingerprintSet
		if err := yaml.Unmarshal(raw, &set); err != nil {
			issues = append(issues, fmt.Sprintf("%s: %v", path, err))
			continue
		}
		for _, is := range CheckFingerprintSet(set) {
			issues = append(issues, fmt.Sprintf("%s: %s", path, is))
		}
		if want := strings.TrimSuffix(filepath.Base(path), ".yaml"); set.ID != want {
			issues = append(issues, fmt.Sprintf("%s: set id %q does not match filename (want %q)", path, set.ID, want))
		}
		sets[set.ID] = set
	}

	// Catalog manifests.
	catDir := filepath.Join(root, "catalog")
	catFiles, err := filepath.Glob(filepath.Join(catDir, "*.yaml"))
	if err != nil {
		return nil, err
	}
	if len(catFiles) == 0 {
		issues = append(issues, fmt.Sprintf("%s: no catalog manifests found", catDir))
	}
	seen := map[string]string{}
	for _, path := range catFiles {
		raw, generic, err := loadYAML(path)
		if err != nil {
			issues = append(issues, fmt.Sprintf("%s: %v", path, err))
			continue
		}
		if err := manifestSchema.Validate(generic); err != nil {
			issues = append(issues, fmt.Sprintf("%s: schema: %v", path, err))
			continue
		}
		var m Manifest
		if err := yaml.Unmarshal(raw, &m); err != nil {
			issues = append(issues, fmt.Sprintf("%s: %v", path, err))
			continue
		}
		if prev, dup := seen[m.ID]; dup {
			issues = append(issues, fmt.Sprintf("%s: duplicate model id %q (also in %s)", path, m.ID, prev))
		}
		seen[m.ID] = path
		if want := strings.TrimSuffix(filepath.Base(path), ".yaml"); m.ID != want {
			issues = append(issues, fmt.Sprintf("%s: model id %q does not match filename (want %q)", path, m.ID, want))
		}
		for _, is := range CheckManifest(m, sets) {
			issues = append(issues, fmt.Sprintf("%s: %s", path, is))
		}
	}
	return issues, nil
}

// CheckManifest runs all cross-checks that the JSON Schema cannot express.
// sets maps fingerprint set id -> set (pass nil to skip reference checks).
func CheckManifest(m Manifest, sets map[string]FingerprintSet) []string {
	var issues []string

	// payout_class vs params_b.
	if b, ok := classParamBounds[m.PayoutClass]; !ok {
		issues = append(issues, fmt.Sprintf("unknown payout_class %q", m.PayoutClass))
	} else if m.ParamsB <= b[0] || m.ParamsB > b[1] {
		issues = append(issues, fmt.Sprintf("payout_class %q does not fit params_b=%.3g (expected (%g, %g])",
			m.PayoutClass, m.ParamsB, b[0], b[1]))
	}

	// MoE sanity: active params, when declared, must be a strict subset of
	// total. (The trust engine's timing envelopes key on this — see the
	// schema's active_params_b description.)
	if m.ActiveParamsB > 0 && m.ActiveParamsB >= m.ParamsB {
		issues = append(issues, fmt.Sprintf("active_params_b=%.3g must be < params_b=%.3g",
			m.ActiveParamsB, m.ParamsB))
	}

	// MoE manifests must declare active_params_b (models#1): without it
	// the trust engine judges an MoE by its total size, reads every honest
	// node serving it as impossibly fast, and five consecutive timing
	// flags slash. Two signals catch the omission — the explicit
	// `architecture` field, and the -a<N>b naming convention.
	switch m.Architecture {
	case "", "dense", "moe":
	default:
		issues = append(issues, fmt.Sprintf("architecture %q must be dense or moe", m.Architecture))
	}
	switch {
	case m.Architecture == "moe" && m.ActiveParamsB <= 0:
		issues = append(issues, "architecture: moe requires active_params_b (parameters active per token); "+
			"the trust engine's timing envelopes will misjudge honest nodes without it")
	case m.Architecture == "dense" && m.ActiveParamsB > 0:
		issues = append(issues, fmt.Sprintf("architecture: dense must not set active_params_b=%.3g "+
			"(drop the field, or set architecture: moe)", m.ActiveParamsB))
	case m.Architecture == "" && m.ActiveParamsB <= 0 && looksMoE(m):
		issues = append(issues, "looks like a Mixture-of-Experts model (id suffix -aNb) but has no active_params_b; "+
			"the trust engine's timing envelopes will misjudge honest nodes — add active_params_b (and architecture: moe)")
	}

	// Pricing table (SPEC §7).
	want := classPricing[m.PayoutClass]
	if m.Embeddings {
		want = embeddingPricing
	}
	if !almostEq(m.BasePayout, want[0]) || !almostEq(m.CustomerPrice, want[1]) {
		issues = append(issues, fmt.Sprintf(
			"pricing (payout=%g, price=%g) does not match §7 table for class %q embeddings=%v (want payout=%g, price=%g)",
			m.BasePayout, m.CustomerPrice, m.PayoutClass, m.Embeddings, want[0], want[1]))
	}
	if m.BasePayout >= m.CustomerPrice {
		issues = append(issues, fmt.Sprintf("base_payout_rate %g must be below customer_price_per_mtok %g", m.BasePayout, m.CustomerPrice))
	}

	// Fingerprint set reference + kind match.
	if sets != nil {
		set, ok := sets[m.FingerprintID]
		if !ok {
			issues = append(issues, fmt.Sprintf("fingerprint_set_id %q has no file in fingerprints/prompts/", m.FingerprintID))
		} else {
			wantKind := "generation"
			if m.Embeddings {
				wantKind = "embedding"
			}
			if set.Kind != wantKind {
				issues = append(issues, fmt.Sprintf("fingerprint set %q has kind %q, model requires %q", m.FingerprintID, set.Kind, wantKind))
			}
		}
	}

	// Per-quant checks.
	quantSeen := map[string]bool{}
	for _, q := range m.Quants {
		prefix := fmt.Sprintf("quant %s", q.Quant)
		if quantSeen[q.Quant] {
			issues = append(issues, fmt.Sprintf("%s: duplicate quant entry", prefix))
		}
		quantSeen[q.Quant] = true
		issues = append(issues, prefixAll(prefix, CheckQuant(q))...)
	}
	return issues
}

// CheckQuant validates one quant entry: naming, URL consistency, sha256
// shape, and min_vram/min_ram sanity relative to the artifact size.
func CheckQuant(q Quant) []string {
	var issues []string

	if !quantNameRE.MatchString(q.Quant) {
		issues = append(issues, fmt.Sprintf("quant name %q is not a canonical llama.cpp quant name", q.Quant))
	}
	if len(q.Parts) == 0 {
		issues = append(issues, checkSingleFile(q)...)
	} else {
		issues = append(issues, checkParts(q)...)
	}
	if q.Mmproj != nil {
		issues = append(issues, prefixAll("mmproj", checkMmproj(*q.Mmproj))...)
	}

	// min_vram sanity: the artifact must fit, with headroom for KV cache, and
	// must not be absurdly padded (>4x artifact size suggests a typo).
	// size_bytes is the total across parts (checkParts enforces the sum), so
	// a sharded model is judged on what it actually occupies in memory.
	sizeMB := (q.SizeBytes + (1 << 20) - 1) >> 20
	if q.MinVRAMMB < sizeMB {
		issues = append(issues, fmt.Sprintf("min_vram_mb %d is below the artifact size (%d MB) — model cannot fit", q.MinVRAMMB, sizeMB))
	}
	if q.MinVRAMMB > sizeMB*4 {
		issues = append(issues, fmt.Sprintf("min_vram_mb %d is more than 4x the artifact size (%d MB) — suspicious", q.MinVRAMMB, sizeMB))
	}
	if q.MinRAMMB < sizeMB {
		issues = append(issues, fmt.Sprintf("min_ram_mb %d is below the artifact size (%d MB) — CPU/unified serving cannot fit", q.MinRAMMB, sizeMB))
	}

	for hw, env := range q.TokS {
		if env.Min <= 0 || env.Max <= 0 || env.Min > env.Max {
			issues = append(issues, fmt.Sprintf("tok_s_estimates[%s]: invalid envelope min=%g max=%g", hw, env.Min, env.Max))
		}
	}
	return issues
}

// checkSingleFile validates the artifact_url/sha256 pair of a quant that
// has no parts.
func checkSingleFile(q Quant) []string {
	var issues []string
	if !strings.Contains(strings.ToLower(q.ArtifactURL), strings.ToLower(q.Quant)) {
		issues = append(issues, fmt.Sprintf("artifact_url %q does not contain quant name %q", q.ArtifactURL, q.Quant))
	}
	if !strings.HasSuffix(q.ArtifactURL, ".gguf") {
		issues = append(issues, fmt.Sprintf("artifact_url %q is not a .gguf file", q.ArtifactURL))
	}
	// Someone catalogued shard 1 of a sharded release alone: llama.cpp
	// would look for the siblings and flockd would never download them.
	if seriesRE.MatchString(path.Base(q.ArtifactURL)) {
		issues = append(issues, fmt.Sprintf("artifact_url %q is one shard of a sharded GGUF — list every shard under parts instead", q.ArtifactURL))
	}
	if q.SHA256 != shaPlaceholder && !sha256RE.MatchString(q.SHA256) {
		issues = append(issues, fmt.Sprintf("sha256 %q is neither 64 lowercase hex chars nor %q", q.SHA256, shaPlaceholder))
	}
	return issues
}

// checkParts validates a sharded quant: no single-file fields, every part
// pinned, the -NNNNN-of-NNNNN series complete and in order in one
// directory, and size_bytes equal to the sum of the parts.
func checkParts(q Quant) []string {
	var issues []string
	if q.ArtifactURL != "" || q.SHA256 != "" {
		issues = append(issues, "parts and artifact_url/sha256 are mutually exclusive — a sharded quant carries only parts")
	}
	if len(q.Parts) < 2 {
		issues = append(issues, "parts needs at least 2 entries — a single file belongs in artifact_url")
	}

	var (
		sum        int64
		wantN      int
		wantPrefix string
		wantDir    string
	)
	for i, p := range q.Parts {
		pfx := fmt.Sprintf("parts[%d]", i)
		sum += p.SizeBytes
		if p.SizeBytes <= 0 {
			issues = append(issues, fmt.Sprintf("%s: size_bytes must be positive", pfx))
		}
		if p.SHA256 != shaPlaceholder && !sha256RE.MatchString(p.SHA256) {
			issues = append(issues, fmt.Sprintf("%s: sha256 %q is neither 64 lowercase hex chars nor %q", pfx, p.SHA256, shaPlaceholder))
		}
		if !strings.Contains(strings.ToLower(p.URL), strings.ToLower(q.Quant)) {
			issues = append(issues, fmt.Sprintf("%s: url %q does not contain quant name %q", pfx, p.URL, q.Quant))
		}
		dir, base := splitURL(p.URL)
		m := seriesRE.FindStringSubmatch(base)
		if m == nil {
			issues = append(issues, fmt.Sprintf("%s: url basename %q is not of the form <name>-NNNNN-of-NNNNN.gguf", pfx, base))
			continue
		}
		idx, _ := strconv.Atoi(m[2])
		n, _ := strconv.Atoi(m[3])
		if i == 0 {
			wantN, wantPrefix, wantDir = n, m[1], dir
			if n != len(q.Parts) {
				issues = append(issues, fmt.Sprintf("series says %d parts (-of-%s) but %d are listed", n, m[3], len(q.Parts)))
			}
		} else {
			if n != wantN {
				issues = append(issues, fmt.Sprintf("%s: series count -of-%s differs from parts[0] (-of-%05d)", pfx, m[3], wantN))
			}
			if m[1] != wantPrefix {
				issues = append(issues, fmt.Sprintf("%s: basename prefix %q differs from parts[0] (%q)", pfx, m[1], wantPrefix))
			}
			if dir != wantDir {
				issues = append(issues, fmt.Sprintf("%s: %q is not in the same directory as parts[0] (%q)", pfx, p.URL, wantDir))
			}
		}
		if idx != i+1 {
			issues = append(issues, fmt.Sprintf("%s: expected shard %05d, got %s — parts must be listed in series order with no gaps", pfx, i+1, m[2]))
		}
	}
	if sum != q.SizeBytes {
		issues = append(issues, fmt.Sprintf("size_bytes %d is not the sum of parts (%d)", q.SizeBytes, sum))
	}
	return issues
}

// checkMmproj validates the vision projector sidecar's shape and pin.
func checkMmproj(p Part) []string {
	var issues []string
	_, base := splitURL(p.URL)
	if !mmprojRE.MatchString(base) {
		issues = append(issues, fmt.Sprintf("url basename %q is not an mmproj*.gguf projector file", base))
	}
	if p.SHA256 != shaPlaceholder && !sha256RE.MatchString(p.SHA256) {
		issues = append(issues, fmt.Sprintf("sha256 %q is neither 64 lowercase hex chars nor %q", p.SHA256, shaPlaceholder))
	}
	if p.SizeBytes <= 0 {
		issues = append(issues, "size_bytes must be positive")
	}
	return issues
}

// splitURL returns the URL with its last path element removed and that
// last element, so two parts can be compared for "same directory".
func splitURL(raw string) (dir, base string) {
	u, err := url.Parse(raw)
	if err != nil || u.Path == "" {
		return path.Dir(raw), path.Base(raw)
	}
	dir = u.Scheme + "://" + u.Host + path.Dir(u.Path)
	return dir, path.Base(u.Path)
}

// CheckFingerprintSet validates determinism requirements and prompt shape
// beyond what the schema enforces.
func CheckFingerprintSet(s FingerprintSet) []string {
	var issues []string
	if s.Kind == "generation" {
		if s.Defaults == nil {
			issues = append(issues, "generation set missing defaults")
		} else {
			if s.Defaults.Temperature != 0 {
				issues = append(issues, fmt.Sprintf("generation set must use temperature 0, got %g", s.Defaults.Temperature))
			}
			if s.Defaults.MaxTokens <= 0 || s.Defaults.MaxTokens > 128 {
				issues = append(issues, fmt.Sprintf("defaults.max_tokens %d out of (0,128]", s.Defaults.MaxTokens))
			}
		}
	}
	ids := map[string]bool{}
	for _, p := range s.Prompts {
		if ids[p.ID] {
			issues = append(issues, fmt.Sprintf("duplicate prompt id %q", p.ID))
		}
		ids[p.ID] = true
		switch s.Kind {
		case "generation":
			if p.Prompt == "" {
				issues = append(issues, fmt.Sprintf("prompt %q: generation prompts need a non-empty 'prompt'", p.ID))
			}
			if p.Input != "" {
				issues = append(issues, fmt.Sprintf("prompt %q: generation prompts must not set 'input'", p.ID))
			}
		case "embedding":
			if p.Input == "" {
				issues = append(issues, fmt.Sprintf("prompt %q: embedding probes need a non-empty 'input'", p.ID))
			}
			if p.Prompt != "" {
				issues = append(issues, fmt.Sprintf("prompt %q: embedding probes must not set 'prompt'", p.ID))
			}
		}
	}
	return issues
}

func compileSchema(path string) (*jsonschema.Schema, error) {
	c := jsonschema.NewCompiler()
	return c.Compile(path)
}

// loadYAML returns the raw bytes and a generic JSON-typed value (via a
// YAML→JSON round-trip) suitable for jsonschema validation.
func loadYAML(path string) ([]byte, any, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, err
	}
	var v any
	if err := yaml.Unmarshal(raw, &v); err != nil {
		return nil, nil, fmt.Errorf("yaml: %w", err)
	}
	jb, err := json.Marshal(v)
	if err != nil {
		return nil, nil, fmt.Errorf("yaml->json: %w", err)
	}
	generic, err := jsonschema.UnmarshalJSON(bytes.NewReader(jb))
	if err != nil {
		return nil, nil, err
	}
	return raw, generic, nil
}

func almostEq(a, b float64) bool { return math.Abs(a-b) < 1e-9 }

func prefixAll(prefix string, in []string) []string {
	out := make([]string, 0, len(in))
	for _, s := range in {
		out = append(out, prefix+": "+s)
	}
	return out
}
