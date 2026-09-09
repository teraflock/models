package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"gopkg.in/yaml.v3"
)

// flatModel is one entry of the catalog flockd consumes
// (flockd/internal/models/manifest.go CatalogModel): every (model, quant)
// pair flattened to a single record keyed `<id>-<quant lowercased>` — the
// same key the coordinator derives (control-plane registry.LoadCatalogDir),
// so a node, the scheduler and this repo always name a servable artifact
// identically.
type flatModel struct {
	ID            string  `json:"id"`
	DisplayName   string  `json:"display_name"`
	Family        string  `json:"family"`
	ParamsB       float64 `json:"params_b"`
	Quant         string  `json:"quant"`
	SHA256        string  `json:"sha256"`
	MinVRAMMB     int64   `json:"min_vram_mb"`
	MinRAMMB      int64   `json:"min_ram_mb"`
	License       string  `json:"license"`
	ArtifactURL   string  `json:"artifact_url"`
	SizeBytes     int64   `json:"size_bytes"`
	PayoutClass   string  `json:"payout_class"`
	ContextLength int     `json:"context_length"`
	Embeddings    bool    `json:"embeddings"`
	// Parts is set for sharded artifacts (then ArtifactURL is empty and
	// SHA256 is the composite id, CompositeSHA256); Mmproj is the optional
	// vision projector sidecar. Both mirror flock.types.v1.ModelSpec.
	Parts  []Part `json:"parts,omitempty"`
	Mmproj *Part  `json:"mmproj,omitempty"`
}

// EmitFlat renders catalog/*.yaml as the flat JSON document published to
// the downloads bucket for nodes (models.manifest_url). Quants whose
// sha256 is still TODO-verify are skipped — a node must never be handed an
// artifact it cannot verify. For sharded quants that means every part (and
// the mmproj sidecar, if declared) must be pinned; a quant with one
// unverified part is skipped whole rather than emitted without it.
func EmitFlat(root string) ([]byte, error) {
	files, err := filepath.Glob(filepath.Join(root, "catalog", "*.yaml"))
	if err != nil {
		return nil, err
	}
	sort.Strings(files)
	var out []flatModel
	for _, f := range files {
		raw, err := os.ReadFile(f)
		if err != nil {
			return nil, err
		}
		var m Manifest
		if err := yaml.Unmarshal(raw, &m); err != nil {
			return nil, fmt.Errorf("%s: %w", f, err)
		}
		for _, q := range m.Quants {
			if !quantVerified(q) {
				continue
			}
			fm := flatModel{
				ID:            m.ID + "-" + lowerQuant(q.Quant),
				DisplayName:   m.DisplayName + " · " + q.Quant,
				Family:        m.Family,
				ParamsB:       m.ParamsB,
				Quant:         q.Quant,
				SHA256:        q.SHA256,
				MinVRAMMB:     q.MinVRAMMB,
				MinRAMMB:      q.MinRAMMB,
				License:       m.License.Name,
				ArtifactURL:   q.ArtifactURL,
				SizeBytes:     q.SizeBytes,
				PayoutClass:   m.PayoutClass,
				ContextLength: m.ContextLength,
				Embeddings:    m.Embeddings,
				Mmproj:        q.Mmproj,
			}
			if len(q.Parts) > 0 {
				fm.Parts = q.Parts
				fm.SHA256 = CompositeSHA256(q.Parts)
			}
			out = append(out, fm)
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no verifiable quants found under %s/catalog", root)
	}
	return json.MarshalIndent(map[string]any{"models": out}, "", "  ")
}

// quantVerified reports whether every hash a node would need to check is
// real: the single-file sha256, or every part's, plus the mmproj's if one
// is declared.
func quantVerified(q Quant) bool {
	if len(q.Parts) == 0 {
		if !sha256RE.MatchString(q.SHA256) {
			return false
		}
	} else {
		for _, p := range q.Parts {
			if !sha256RE.MatchString(p.SHA256) {
				return false
			}
		}
	}
	if q.Mmproj != nil && !sha256RE.MatchString(q.Mmproj.SHA256) {
		return false
	}
	return true
}

func lowerQuant(q string) string {
	b := []byte(q)
	for i, c := range b {
		if c >= 'A' && c <= 'Z' {
			b[i] = c + 32
		}
	}
	return string(b)
}
