# teraflock/models

Open-source model catalog and public fingerprint challenge sets for the
[Teraflock](../README.md) mesh. Apache-2.0.

This repo is data, not code (plus a small Go validator). It is the source of
truth for **which models the mesh serves, at which quants, at what prices, and
how they are fingerprinted** (SPEC §4.6, §7, §2.2, A2.4).

## Layout

```
catalog/                      one YAML manifest per model
fingerprints/prompts/         public challenge prompt sets
schema/manifest.schema.json   JSON Schema for catalog manifests
schema/fingerprint-prompts.schema.json
tools/validate/               Go validator (schema + cross-checks), run in CI
```

## Catalog manifests

Each `catalog/<model-id>.yaml` supplies everything `flock.types.v1.ModelSpec`
needs (see the `proto` repo) plus catalog-only metadata:

- **Identity:** `id`, `family`, `params_b`, `context_length`, `embeddings`.
- **License:** real license name/URL/notes. Llama-family models are under the
  Llama Community License — commercial serving is permitted but carries
  attribution/AUP/700M-MAU conditions; a legal pass on the catalog is required
  before commercial launch (SPEC §13.2). Everything else in the current
  catalog is Apache-2.0.
- **Economics:** `payout_class` (nano/small/mid/large) and the SPEC §7 prices:
  `customer_price_per_mtok` and `base_payout_rate` (USD per million tokens,
  payout ≈ 55% of price). Embedding models are priced separately (~$0.01/Mtok).
- **Per-quant artifacts:** `quant` (canonical llama.cpp name), `artifact_url`
  (real upstream GGUF URL; production nodes fetch through our HF-proxying CDN),
  `sha256` + `size_bytes` (verified against the upstream host; `flockd` refuses
  to serve on hash mismatch), `min_vram_mb` / `min_ram_mb`, and
  `tok_s_estimates` — **rough, honest single-stream decode envelopes per
  hardware class** (`m2-max`, `rtx-4090`, `rtx-3060`, `cpu-avx2`). These are
  estimates, not benchmarks; the trust engine uses them only as coarse timing
  sanity bounds (SPEC §2.2) and they will be replaced by fleet-measured
  percentiles. A hardware class is omitted when the quant cannot reasonably
  run on it (e.g. 32B on a 12GB 3060).

Current catalog: `llama-3.2-3b-instruct`, `qwen2.5-1.5b-instruct` (nano);
`llama-3.1-8b-instruct`, `qwen2.5-7b-instruct` (small); `qwen2.5-32b-instruct`,
`mistral-small-24b-instruct-2501` (mid); `llama-3.3-70b-instruct` (large);
`nomic-embed-text-v1.5` (embeddings).

## Fingerprints: the trust-model split

Model fingerprinting (SPEC §2.2) detects nodes serving a different model,
quant, or runtime than advertised:

1. **Challenge prompts are public** — they live here, in
   `fingerprints/prompts/<set-id>.yaml`. Publishing them costs nothing: they
   are only useful together with the expected outputs.
2. **Expected outputs are private** — precomputed by the control plane on
   reference hardware and stored in the private `control-plane` repo, keyed by
   the tuple **`(model_sha, quant, runtime_build_id)`**. Greedy decoding
   (temperature 0) with a fixed seed and short `max_tokens` makes the output
   deterministic for a given tuple, so an honest node reproduces it exactly.
   A cheater cannot precompute answers without owning the exact same tuple —
   at which point they are doing the work honestly anyway.
3. `runtime_build_id` comes from the private `runtimes` repo's pinned
   llama.cpp builds, because different llama.cpp versions/backends can produce
   different (still deterministic) token streams.

Prompt sets are designed to discriminate: arithmetic chains (quantization
error compounds across steps), rare-token continuations (tokenizer- and
tail-distribution-sensitive), and strict format-following. Embedding models
use probe inputs compared by cosine similarity against private reference
vectors.

Sets are versioned (`fp-gen-v1`, `fp-gen-v2`, `fp-embed-v1`) and rotated by
publishing a new set and flipping `fingerprint_set_id` in the manifests.

## Validation

```sh
cd tools/validate
go test ./...        # unit + integration (validates the committed catalog)
go run . -root ../.. # what CI runs
```

The validator checks every manifest against the JSON Schemas and then
cross-checks what a schema cannot express: `payout_class` vs `params_b`
ranges, exact SPEC §7 pricing per class (with the embeddings override),
`min_vram_mb`/`min_ram_mb` sanity vs artifact size, canonical quant naming and
quant-name/URL consistency, sha256 shape (real 64-hex or an explicit
`TODO-verify` — never a plausible-looking fake), fingerprint set references
and generation/embedding kind match, filename/id agreement, and fingerprint
set determinism rules (greedy, bounded `max_tokens`, unique prompt ids).

## Adding a model

1. Create `catalog/<model-id>.yaml` (copy a neighbor of the same class).
2. Pull real `sha256`/`size_bytes` from the artifact host (for Hugging Face:
   `https://huggingface.co/api/models/<repo>/tree/main` exposes the LFS
   sha256), or mark them `TODO-verify` — CI accepts the placeholder on
   branches; release policy does not.
3. Pick the §7 `payout_class` and copy its exact prices.
4. Assign a `fingerprint_set_id`; ask the control-plane team to generate
   expected outputs for every (quant × runtime_build_id) before the model is
   schedulable.
5. `cd tools/validate && go run . -root ../..` must print `catalog OK`.
