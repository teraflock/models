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
  `context_length`, `embeddings`, `decision` (see
  [Decision models](#decision-models)), and for Mixture-of-Experts models
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
  their own row (input tokens only). Decision models have no row of their
  own: they copy their class row unchanged and are billed on input tokens
  at the class input rate. The validator rejects any value off the
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
  run on it (e.g. 32B on a 12GB 3060). Models that decode nothing reuse the
  field with a different meaning, stated in the manifest header: batched
  embedding tokens/sec for embedding models, prompt-processing tokens/sec
  for decision models.

Current catalog: `llama-3.2-3b-instruct`, `qwen2.5-1.5b-instruct` (nano);
`llama-3.1-8b-instruct`, `qwen2.5-7b-instruct` (small); `qwen2.5-32b-instruct`,
`mistral-small-24b-instruct-2501` (mid); `llama-3.3-70b-instruct` (large);
`nomic-embed-text-v1.5` (embeddings); `julia-1`, `laya`, `kev-4b`,
`clef-flash`, `clef` (decision). The `catalog/` directory is the full list.

### Decision models

A manifest with `decision: true` is a **typed decision model**: it is served
via `POST /v1/systemone`, answers `choice` / `score` / `noul` questions
about a state with probabilities, and generates no tokens. The contract
(public API, wire, semantics) is the proto design note
`2026-10-03-decision-models`; `decision` mirrors
`flock.types.v1.ModelSpec.decision`.

- `decision` is an optional boolean, default `false`, so existing manifests
  do not change. It is **mutually exclusive with `embeddings: true`** (schema
  and validator both refuse the pair). `embeddings` stays required and is
  `false` on a decision manifest.
- **Pricing:** the `payout_class` row, copied unchanged like any chat model
  of that class (SPEC §7, "Decision models"). Only the input price is ever
  charged; `price_out_per_mtok` is still the class value, not zero and not
  omitted, so the ledger formula and every reader of the catalog stay as
  they are. There is no flat decision row.
- **Fingerprints:** `fingerprint_set_id` must name a set of kind `decision`
  (`fp-decision-v1`).
- **Artifacts:** GGUF, from the `ggml-org/*-GGUF` conversions, under the same
  quant, hash and size rules as everything else. Encoder-sized models list
  `Q8_0` first (the `flock/<id>` default) and `BF16` as the canary-reference
  quant; a model whose `BF16` file is out of proportion to its class lists
  `Q8_0` as the highest-fidelity quant instead and says so in its header.
- **`context_length`** is the window the model is served at, taken from the
  source model card, which for these models is often far below the
  architecture maximum in the GGUF metadata (Laya: 512 against 8,192).
- **`tok_s_estimates`** is required by the schema but there is no decode
  speed to estimate. Decision manifests put **prompt-processing throughput**
  there: input tokens evaluated per second within one request, per hardware
  class, as rough estimates. It is the same move embedding manifests make
  (batched embedding tokens/sec), it keeps the field's unit (tokens/sec) and
  shape, and it is the only speed a decision request has. Nothing may read
  it as a decode envelope: decision requests are exempt from the trust
  engine's timing checks, as embeddings are.
- They need a runtime build with llama.cpp >= b11382; a node on an older
  build cannot load the artifact.

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

Decision models use sets of kind `decision`. A probe is a `/v1/systemone`
request body without `model`: a fixed `state` (a string, or structured JSON
written as YAML) and `questions` in the public shape.

```yaml
id: fp-decision-v1
kind: decision          # no `defaults`: nothing is decoded
description: ...
prompts:                # 8..20 probes
  - id: multi-01
    category: decision-probe
    state: "Hi, we were billed twice for March ..."   # or a mapping / list
    questions:          # 1..64, id -> question
      department:
        type: choice
        instructions: Which department should handle this?
        criteria:       # 2..20 options: description, or null for none
          billing: Invoices, payments, refunds
          other: null
      urgency:
        type: score
        instructions: How urgent is this?
        criteria: [not urgent, soon, blocking]        # 2..10 levels, lowest first
      escalate:
        type: noul
        instructions: Does this need a human within the hour?
        criteria:       # optional; keys must be quoted or YAML reads booleans
          "true": ...
          "false": ...
```

**Order is part of the probe.** Questions, and the options inside
`criteria`, reach the model in the order written, so a consumer must read
those mappings in document order (a `yaml.Node`, never a Go map) and
serialise structured states as compact JSON in that same order. The private
half is the expected **probabilities** per `(model_sha, quant,
runtime_build_id)`, compared within an absolute tolerance per probability
and never by hash, because backends differ in the last bits. Probabilities,
answers or any other expected output never go in this repo; the schema
rejects unknown fields on a probe and on a question. One set has to run on
every decision model in the catalog, so probes stay inside the tightest
native limits among them: English, short enough for a 512-token window, and
at most 20 options per choice (the public API allows 255).

Sets are versioned (`fp-gen-v1`, `fp-gen-v2`, `fp-embed-v1`,
`fp-decision-v1`) and rotated by publishing a new set and flipping
`fingerprint_set_id` in the manifests.

## Validation

```sh
cd tools/validate
go test ./...                 # unit + integration (validates the committed catalog)
go run . -root ../..          # what CI runs on every branch
go run . -root ../.. -release # what promote.yml runs: TODO-verify is an error
```

The validator checks every manifest against the JSON Schemas and then
cross-checks what a schema cannot express: `payout_class` vs `params_b`
ranges, exact SPEC §7 pricing per class (with the embeddings override; decision
models take the class row), `embeddings`/`decision` mutual exclusion,
`active_params_b` present on Mixture-of-Experts manifests (`architecture:
moe` or the `-a<N>b` id suffix) and absent on `architecture: dense`,
`min_vram_mb`/`min_ram_mb` sanity vs artifact size, canonical quant naming and
quant-name/URL consistency, sha256 shape (real 64-hex or an explicit
`TODO-verify` — never a plausible-looking fake), fingerprint set references
and generation/embedding/decision kind match, filename/id agreement,
fingerprint set determinism rules (greedy, bounded `max_tokens`, unique
prompt ids), and decision probes against the public `/v1/systemone` request
limits.

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
6. A decision model (`/v1/systemone`)? Set `decision: true`, keep
   `embeddings: false`, point `fingerprint_set_id` at a `decision` set, take
   `context_length` from the source model card, and fill `tok_s_estimates`
   with prompt-processing tokens/sec (see [Decision models](#decision-models)).
7. `cd tools/validate && go run . -root ../..` must print `catalog OK`.

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
