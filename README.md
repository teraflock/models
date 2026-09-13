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

- **Identity:** `id`, `display_name`, `family`, `params_b` (TOTAL),
  `context_length`, `embeddings`, and for Mixture-of-Experts models
  `active_params_b` (parameters active per token) plus `architecture: moe`.
  The trust engine's timing envelopes key on active params: an MoE
  without `active_params_b` reads every honest node serving it as
  impossibly fast, so the validator rejects an MoE manifest (by
  `architecture: moe` or the `-a<N>b` id suffix) that omits it. Pricing
  still keys on total params.
- **License:** real license name/URL/notes. [`LICENSES.md`](LICENSES.md) is
  the licensing pass over the catalog (2026-09-13), organised by license
  family: Llama Community License (commercial serving permitted; attribution,
  AUP and notice obligations, which Teraflock carries), Gemma 4 and everything
  else Apache-2.0, DeepSeek MIT. `notes` names the family and any caveat; a
  model under new or bespoke terms needs a section there before it merges.
- **Economics:** `payout_class` (nano/small/mid/large/xl, by TOTAL params)
  and the SPEC §7 row for it, copied verbatim: `price_in_per_mtok` and
  `price_out_per_mtok` (interactive USD per million input / output tokens;
  batch is 0.5× of both) and `payout_share` (the share of the charge paid to
  the serving node, rising with hardware scarcity). Embedding models carry
  their own row (input tokens only). The validator rejects any value off the
  table — prices change in the SPEC first, then here.
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
go test ./...                 # unit + integration (validates the committed catalog)
go run . -root ../..          # what CI runs on every branch
go run . -root ../.. -release # what promote.yml runs: TODO-verify is an error
```

The validator checks every manifest against the JSON Schemas and then
cross-checks what a schema cannot express: `payout_class` vs `params_b`
ranges, exact SPEC §7 pricing per class (with the embeddings override),
`active_params_b` present on Mixture-of-Experts manifests (`architecture:
moe` or the `-a<N>b` id suffix) and absent on `architecture: dense`,
`min_vram_mb`/`min_ram_mb` sanity vs artifact size, canonical quant naming and
quant-name/URL consistency, sha256 shape (real 64-hex or an explicit
`TODO-verify` — never a plausible-looking fake), fingerprint set references
and generation/embedding kind match, filename/id agreement, and fingerprint
set determinism rules (greedy, bounded `max_tokens`, unique prompt ids).

`-release` adds the publishing gate: every `TODO-verify` (single-file
sha256, any part, any mmproj) is reported as an issue and `-emit-flat`
refuses to write. Without `-release` an unverified quant is merely left out
of the emitted catalog. CI runs the release check on every branch as an
informational step so a PR can see what would block a tag.

## Publishing

Two objects live on the downloads bucket under
`https://teraflock-downloads.s3.amazonaws.com/catalog/`, both the flat
`-emit-flat` document (one `<id>-<quant>` entry per servable artifact):

| object | written by | who reads it |
|---|---|---|
| `catalog-staging.json` | every merge to `main` (`ci.yml`, `publish-staging`) | canary / dev nodes: set `models.manifest_url` to it in flockd's config |
| `catalog.json` | `promote.yml` only, after approval | flockd's default `models.manifest_url` — every fresh install |

Merging to main is therefore not publishing. Quants with `TODO-verify`
hashes are left out of the staging object; the stable object cannot be
written at all while any remain (release-mode validation).

### Release: tag → promote → stable

1. Make sure `cd tools/validate && go run . -root ../.. -release` prints
   `catalog OK` on `main` (the `release readiness` step of the last CI run
   says the same). If it lists placeholders, fetch the real hashes from the
   artifact host first — never invent one.
2. Tag the catalog: `vYYYY.MM.N`, the year/month of the release and a
   counter within the month (`git tag v2026.09.1 && git push origin
   v2026.09.1`). The catalog is data, so it is dated rather than
   semver'd; never move a tag — a fix gets the next `N`.
3. The tag runs `.github/workflows/promote.yml`, which waits in the
   `stable` GitHub environment for its required reviewer's approval (the
   audit trail). The reviewer checklist is at the top of the workflow:
   nothing unexpected in the added/removed entries, fingerprint expected
   outputs exist for every new `(model_sha, quant)`, a canary served the
   staging object, and no pricing moved without a SPEC §7 change.
4. On approval the job re-runs the tests, validates in release mode, emits
   `catalog.json`, checks every hash in it is a 64-hex digest, writes the
   step summary (entries added / removed / hash-changed versus the current
   stable object), uploads to `catalog/catalog.json` and reads the public
   URL back to confirm it is byte-equal.

Rolling back is the same workflow run manually (Actions → **promote** →
*Run workflow*, or `gh workflow run promote.yml --repo teraflock/models
--ref v2026.09.1`) on the previous tag. The uploader credentials are
write-only on the bucket; both workflows read the objects back over the
public URL.

Consumers that do **not** go through these objects: the control plane's
registry reads `catalog/` straight from a checkout
(`FLOCK_REGISTRY__CATALOG_PATH`), so its view of the catalog is whatever
ref it was deployed from, not the stable object.

## Adding a model

1. Create `catalog/<model-id>.yaml` (copy a neighbor of the same class).
2. Pull real `sha256`/`size_bytes` from the artifact host (for Hugging Face:
   `https://huggingface.co/api/models/<repo>/tree/main` exposes the LFS
   sha256), or mark them `TODO-verify` — CI accepts the placeholder on
   branches and main (the quant is left out of the staging object);
   `promote.yml` refuses to write the stable object while any remain.
3. Pick the §7 `payout_class` and copy its exact row — `price_in_per_mtok`,
   `price_out_per_mtok`, `payout_share` (classed by TOTAL `params_b`, MoE
   included).
4. Set `architecture: dense | moe`. MoE? Set `active_params_b` (parameters
   active per token) — the validator refuses an MoE manifest without it,
   because the trust engine would otherwise flag honest nodes for being
   "impossibly" fast.
5. Assign a `fingerprint_set_id`. Expected outputs are generated by the
   control plane itself once two first-party nodes serve the model on a
   runtime build (the coordinator's fingerprint loop); until then nodes
   serving it on that build are not fingerprint-challenged. The control
   plane's `admin fingerprints coverage --runtime-build-id <id>` reports
   the gap per (model, prompt).
6. `cd tools/validate && go run . -root ../..` must print `catalog OK`.

### Quant names and publishers

`quant` is a canonical llama.cpp name (`Q4_K_M`, `IQ4_XS`, `MXFP4`, `F16`,
…) and ggml-org / first-party repos are the default source. unsloth
`UD-*` dynamic quants (`UD-Q4_K_XL` …) are accepted **per model only where
no reputable standard-named quant exists**, with the reason in the
manifest's header comment; such a manifest must have an `unsloth/*`
`source_repo`, and only `UD-Q4` and above are allowed — the class price
buys class quality, and sub-Q4 dynamic quants are measurably worse.

### Multi-part artifacts and vision sidecars

Sharded releases (`<name>-00001-of-0000N.gguf` ...) use `parts` instead of
`artifact_url`/`sha256` — exactly one of the two per quant (schema `oneOf`):

```yaml
  - quant: Q4_K_M
    parts:
      - url: https://huggingface.co/<repo>/resolve/main/<name>-Q4_K_M/<name>-Q4_K_M-00001-of-00003.gguf
        sha256: <lfs.oid of that file>
        size_bytes: <lfs.size>
      - url: .../<name>-Q4_K_M-00002-of-00003.gguf
        ...
      - url: .../<name>-Q4_K_M-00003-of-00003.gguf
        ...
    mmproj:            # optional vision projector, only for VL releases
      url: https://huggingface.co/<repo>/resolve/main/mmproj-F16.gguf
      sha256: <lfs.oid>
      size_bytes: <lfs.size>
    size_bytes: <sum of the parts, mmproj excluded>
```

Rules the validator enforces: every shard listed, in series order, same
directory, exactly `N` entries for `-of-0000N`; quant-level `size_bytes`
equals the sum of the parts; each part and the `mmproj` pinned with a real
sha256 or `TODO-verify`; listing shard 1 alone under `artifact_url` is
rejected. `min_vram_mb`/`min_ram_mb` sanity uses the summed size.

Where the hashes come from: shards usually live in a subdirectory, so query
`https://huggingface.co/api/models/<repo>/tree/main/<subdir>` (or
`tree/main?recursive=true`); each LFS entry carries `lfs.oid` (the SHA-256
to copy) and `lfs.size`. Xet-backed repos also show an `xetHash` — that is
**not** a SHA-256; copy `lfs.oid`. Never paste a hash you did not read from
the host.

The flat catalog (`-emit-flat`) carries `parts` and `mmproj` verbatim, sets
`artifact_url` empty for sharded quants, and fills `sha256` with the
**composite id** — sha256 over the concatenated part hashes in order — which
is the single "quant sha" the mesh pins per dispatch (see the proto design
note `2026-09-08-sharded-artifacts`). A sharded quant with any unverified
part or sidecar is left out of the flat catalog as a whole.
