# Model licensing pass — Teraflock catalog

**Date:** 2026-09-13 · **Scope:** every manifest in `catalog/` · **Status:** self-authored
from the public license texts; not a legal opinion. Teraflock engages counsel
only for the payout/regulatory leg (SPEC §13.3). Per-manifest `license.notes`
derive from this file; when they disagree, fix the manifest.

## What Teraflock does with a model (the fact pattern)

- **Redistribution by reference.** A manifest publishes the upstream artifact
  URL and its sha256; nodes download the GGUF from the upstream host (in
  production through Teraflock's HF-proxying CDN). Teraflock does not modify
  weights, fine-tune, or re-quantise anything. For licensing purposes this
  pass treats the proxy download as *distribution* anyway — the conservative
  reading — so every obligation that attaches to distributing Llama Materials
  or Apache-2.0 works is met as if we hosted the file ourselves.
- **Serving as a service.** Third-party operators run inference on the
  weights; Teraflock meters tokens, charges customers through a hosted
  OpenAI-compatible API, and pays operators. No customer receives weights.
- **Derivatives:** none. Teraflock ships no fine-tunes and no models built on
  model outputs.

Two questions per family: *may we point nodes at the weights?* and *may we
sell tokens generated from them?* — and what each answer obliges us to do.

## Family 1 — Llama Community License (Meta; not OSI open source)

| Model | License | Weights we point at |
|---|---|---|
| `llama-3.1-8b-instruct` | Llama 3.1 Community License Agreement (2024-07-23) | `bartowski/Meta-Llama-3.1-8B-Instruct-GGUF` (tagged `llama3.1`) |
| `llama-3.2-3b-instruct` | Llama 3.2 Community License Agreement (2024-09-25) | `bartowski/Llama-3.2-3B-Instruct-GGUF` (tagged `llama3.2`) |
| `llama-3.3-70b-instruct` | Llama 3.3 Community License Agreement (2024-12-06) | `bartowski/Llama-3.3-70B-Instruct-GGUF` (tagged `llama3.3`) |

The three agreements are the same instrument with the version string changed.
Section 1.a grants a worldwide, non-exclusive, royalty-free licence to "use,
reproduce, distribute, copy, create derivative works of, and make modifications
to the Llama Materials". Hosted serving is a *use*; pointing nodes at a
re-quantised GGUF is a *distribution* of Llama Materials (the quant is a
derivative and carries the same licence — bartowski's repos are tagged with
it).

**Answer:** both redistribution-by-reference and serving-as-a-service are
permitted, subject to the obligations below.

| Obligation (section) | What it requires | How Teraflock meets it |
|---|---|---|
| 1.b.i(A) — copy of the Agreement with distribution | "provide a copy of this Agreement with any such Llama Materials" | Each manifest's `license.url` is the Agreement; the daemon shows it in `tera models` and the website/desktop model pages link it. |
| 1.b.i(B) — attribution | "prominently display 'Built with Llama' on a related website, user interface, blogpost, about page, or product documentation" | teraflock.com footer, app.teraflock.com console footer, and this file. |
| 1.b.iii — notice file | retain the notice text in a "Notice" file distributed with the Materials | The three notice lines below, in this public file. |
| 1.b.iv — Acceptable Use Policy | comply with, and not let others violate, the AUP "incorporated by reference" (`https://developer.meta.com/ai/llama3_1/use-policy/` etc.) | The Teraflock Terms of Service prohibit the AUP's listed uses for every model (website#3 carries the clause); the gateway's abuse controls are the enforcement. |
| 1.b.i — naming | an AI model *built from* Llama Materials must start with "Llama" | n/a: no derivatives. Catalog ids already start with `llama-`. |
| 2 — 700M MAU | request a licence from Meta if, **on the version release date**, the licensee had >700M monthly active users in the prior month | n/a: Teraflock did not exist on 2024-07-23 / 09-25 / 12-06 and the test is fixed to that date. |
| 5 — trademarks | no use of Meta/Llama marks beyond the required attribution | Only the required "Built with Llama" string and the model names. |

Notice lines (section 1.b.iii):

> Llama 3.1 is licensed under the Llama 3.1 Community License, Copyright © Meta Platforms, Inc. All Rights Reserved.
>
> Llama 3.2 is licensed under the Llama 3.2 Community License, Copyright © Meta Platforms, Inc. All Rights Reserved.
>
> Llama 3.3 is licensed under the Llama 3.3 Community License, Copyright © Meta Platforms, Inc. All Rights Reserved.

Sources: `https://developer.meta.com/ai/llama3_1/license/`,
`.../llama3_2/license/`, `.../llama3_3/license/` (fetched 2026-09-13).

## Family 2 — Gemma 4 (Google; Apache-2.0 plus a separate policy)

| Model | License | Weights we point at |
|---|---|---|
| `gemma-4-12b-it` | Apache-2.0 | `ggml-org/gemma-4-12B-it-GGUF`, `google/gemma-4-12B-it-qat-q4_0-gguf`, `unsloth/gemma-4-12b-it-GGUF` |
| `gemma-4-26b-a4b-it` | Apache-2.0 | `ggml-org/gemma-4-26B-A4B-it-GGUF` |
| `gemma-4-31b-it` | Apache-2.0 | `ggml-org/gemma-4-31B-it-GGUF`, `unsloth/gemma-4-31B-it-GGUF` |

Google dropped the bespoke "Gemma Terms of Use" with this generation. Verified
2026-09-13 three ways: Google's licence page
(`https://ai.google.dev/gemma/docs/gemma_4_license`) reproduces the Apache
License 2.0 text as "the Gemma 4 license"; the `google/gemma-4-*` and
`ggml-org/gemma-4-*` Hugging Face repos carry `license: apache-2.0`; none of
them is gated, so no click-through terms are accepted at download.

Google also publishes a **Prohibited Use Policy** (last modified 2024-02-21)
and an **Intended Use Statement** as separate documents linked from that
page. The policy lists prohibited use categories (infringement, illegal or
dangerous activity, harmful content, misinformation, sexually explicit
content) and opens "You may not use nor allow others to use Gemma…", but it
contains no acceptance mechanism and is not incorporated into the licence
text; Apache-2.0 §2 grants use without field-of-use restriction and §4 lists
the only redistribution conditions. It therefore binds as a licence term only
if a downloader accepted it somewhere, which the ungated repos never ask for.

**Answer:** redistribution and serving are permitted on Apache-2.0 terms
(see Family 3 for the obligations, which are the same). Independently of
whether the Prohibited Use Policy binds, the Teraflock Terms of Service
prohibit the same categories of use for every model, so nothing further
flows down to API customers.

> Catalog status: all three Gemma models stay in the catalog (decided
> 2026-09-13 on the analysis above; an earlier plan to remove them had
> assumed non-OSI terms).

## Family 3 — Apache-2.0 (OSI; no NOTICE files upstream)

| Model | Publisher | Weights we point at |
|---|---|---|
| `gpt-oss-20b`, `gpt-oss-120b` | OpenAI | `ggml-org/gpt-oss-*-GGUF`, `unsloth/gpt-oss-20b-GGUF` |
| `qwen2.5-1.5b-instruct`, `qwen2.5-7b-instruct`, `qwen2.5-32b-instruct` | Alibaba Cloud (Qwen) | `bartowski/Qwen2.5-*-Instruct-GGUF` |
| `qwen3-4b-instruct-2507`, `qwen3-8b`, `qwen3-32b`, `qwen3-30b-a3b-instruct-2507`, `qwen3-coder-30b-a3b-instruct`, `qwen3-next-80b-a3b-instruct`, `qwen3-235b-a22b-instruct-2507` | Alibaba Cloud (Qwen) | `ggml-org/Qwen3-32B-GGUF`, `unsloth/Qwen3-*-GGUF` |
| `qwen3.6-35b-a3b`, `qwen3.8-27b` | Alibaba Cloud (Qwen) | `ggml-org/Qwen3.6-35B-A3B-GGUF`, `ggml-org/Qwen3.8-27B-GGUF` |
| `mistral-small-24b-instruct-2501` | Mistral AI | `bartowski/Mistral-Small-24B-Instruct-2501-GGUF` |
| `nomic-embed-text-v1.5` | Nomic AI | `nomic-ai/nomic-embed-text-v1.5-GGUF` |

Qwen2.5 caveat: the 0.5B/1.5B/7B/14B/32B checkpoints are Apache-2.0; the 3B
and 72B are under the Qwen Research / Qwen licences and are **not** in the
catalog. Every Qwen3.x checkpoint above is Apache-2.0.

**Answer:** redistribution and serving are permitted without usage
conditions. Apache-2.0 §4 obligations when distributing:

| Obligation (§4) | How Teraflock meets it |
|---|---|
| (a) give recipients a copy of the License | `license.url` in every manifest; nodes and the model pages link it. |
| (b) notices of modification | n/a: weights are not modified by Teraflock. |
| (c) retain copyright/patent/trademark/attribution notices | Upstream artifacts are fetched byte-for-byte (sha256-pinned), so whatever notices they carry are retained. |
| (d) reproduce the NOTICE file contents, if the Work includes one | **None of the upstream repos ships a NOTICE file** (checked every base and quant repo above via the Hugging Face API, 2026-09-13), so (d) adds nothing. Re-check when adding a model. |

## Family 4 — MIT (OSI)

| Model | License | Weights we point at |
|---|---|---|
| `deepseek-v4-flash-0731` | MIT (Copyright (c) 2023 DeepSeek) | `unsloth/DeepSeek-V4-Flash-0731-GGUF` (`UD-Q4_K_XL`, tagged `mit`) |

**Answer:** redistribution and serving are permitted without usage
conditions. MIT's single obligation — include the copyright and permission
notice with copies — is met by `license.url` (DeepSeek's LICENSE file) and
the notice line here:

> Copyright (c) 2023 DeepSeek. Licensed under the MIT License.

## Adding a model: the licence checklist

1. Record the real licence name and URL in the manifest; `notes` says which
   family above applies and any caveat.
2. Non-OSI or bespoke terms (a "Terms of Use", a research licence, an
   acceptable-use policy incorporated by reference) need a new section here
   before the manifest merges — say what the terms oblige and how Teraflock
   meets it. Llama is the template.
3. Check the upstream repo for a NOTICE file (Apache-2.0 §4(d)) and whether
   the repo is gated (a gated repo means click-through terms that may bind
   downloaders).
4. Check the quant repo's licence tag matches the base model's; a
   re-quantiser cannot change the licence.
5. If the licence requires attribution, add it to the website and console
   footers in the same change.
