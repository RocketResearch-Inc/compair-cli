# Launch Readiness

Use this as the working launch board for the public Compair CLI, Core, and Cloud release. It pulls together the current validation state, the launch recommendation we can already defend, and the remaining work that still needs either engineering follow-through or external setup.

## Current Snapshot

Status as of 2026-05-02:

- Production Cloud: launch-quality validation is in good shape. Seeded scenarios A, B, and C passed in production and now all have archived artifact bundles. Scenario D was inconclusive and should be treated as a softer install-surface sanity check, not a blocker.
- Core with OpenAI embeddings: strict finding-universe baseline is complete and credible. The premium lane currently landed at `0.571` F1 in `compair_eval_runs/finding_universe_metrics/core-openai-embeddings-20260501-final/`.
- Core with local embeddings + local generation: native strict evaluation is complete after excluding dropped notification events. The row is still `0.000` F1 and should be treated as experimental / manual-review assist, not a launch-tier generative lane.
- Core with local embeddings + OpenAI generation: strict replay evaluation is complete. The premium lane landed at `0.560` F1, `gpt-5.4-mini` landed at `0.349`, and `gpt-5.4-nano` landed at `0.250` in `compair_eval_runs/finding_universe_metrics/core-local-openai-generation-20260501-final/`.
- Tuning: the first `core-openai-embeddings` tuning pass overfit the training split and is not ready to promote.

Recommended launch framing:

- Cloud is the strongest out-of-the-box experience and the primary launch-tier path.
- Core with OpenAI embeddings + OpenAI generation is the recommended self-hosted quality path.
- Core with local embeddings + OpenAI generation is the lower-outsourced-cost self-hosted path. It stays credible on the premium lane, but the smaller model lanes are more variable than the Cloud flagship.
- Fully local Core is the privacy-first, zero-external-cost path. Today it is better framed as retrieval assistance for manual review, with experimental native generation.

## Current Messaging Matrix

| Profile family | Retrieval | Generation | Current evidence | Recommended messaging |
| --- | --- | --- | --- | --- |
| Cloud flagship | Cloud hybrid + OpenAI 1536 embeddings | OpenAI (`gpt-5.4-mini` default, `gpt-5.4` premium) | Validated flagship strict benchmark and production seeded launch validation | Primary launch-tier path; strongest out-of-the-box experience |
| Core + OpenAI embeddings + OpenAI generation | Local search + OpenAI 1536 embeddings | OpenAI replay models | `compair_gpt54` strict F1 `0.571` in `core-openai-embeddings-20260501-final` | Recommended self-hosted quality path |
| Core + local embeddings + OpenAI generation | Local search + local embeddings | OpenAI replay models | `compair_gpt54` strict F1 `0.560`; `gpt-5.4-mini` `0.349`; `gpt-5.4-nano` `0.250` in `core-local-openai-generation-20260501-final` | Secondary self-hosted path with no outsourced embeddings; credible premium lane, weaker budget-lane stability |
| Core + local embeddings + local generation | Local search + local embeddings | Local heuristic generation | Native strict F1 `0.000` after drop-filtered rerun; retrieval still shows useful signal | Experimental / manual-review assist, not a headline generative-quality lane |

## Retrieval Coverage Snapshot

These numbers come from the reference-family audit artifacts, not the generative finding-universe scorer. They are the right way to talk about the fully local path when we want to measure whether it surfaces review-worthy neighborhoods at all.

| Profile family | Positive families with selected refs | Positive families seen in trace | Notes |
| --- | ---: | ---: | --- |
| Cloud flagship | `10/20` (`50%`) | `13/20` (`65%`) | Broadest retrieval coverage in the current artifact set |
| Core + OpenAI embeddings | `6/13` (`46%`) | `9/13` (`69%`) | Solid self-hosted retrieval coverage |
| Core + local embeddings | `4/13` (`31%`) | `9/13` (`69%`) | Meaningful surfacing signal, but weaker final selection than the OpenAI-embedding lane |

Interpretation:

- “selected refs” means at least one labeled-positive family had a reference make the final evidence set
- “seen in trace” means a labeled-positive family appeared as a candidate or selected reference somewhere in retrieval
- the Cloud flagship artifact spans a broader labeled family set than the two Core runs, so use the percentages and the qualitative direction, not the raw totals alone

## Evidence We Already Have

- Seeded launch validation is documented in [launch_validation.md](launch_validation.md).
- The evaluation methodology is summarized in [quality_evaluation.md](quality_evaluation.md).
- The validated Cloud flagship comparison is summarized in `compair_cloud/docs/reference_profile_flagship_results.md`.
- The canonical cross-profile comparison is summarized in `compair_cloud/docs/reference_profile_results_matrix.md`.
- The reference profile evaluation workflow is documented in `compair_cloud/docs/reference_profile_playbook.md`.
- The release artifact and package-manager automation checklist already exists in [release_checklist.md](release_checklist.md) and [ci_release.md](ci_release.md).
- Retrieval-family audit artifacts now exist for the Cloud flagship, `core-openai-embeddings`, and `core-local` runs, so we can support a retrieval-only comparison table even where native generation quality is not launch-grade.

## Completed Engineering Prep

- Production Cloud seeded reruns were completed with a stable procedure:
  - fresh `HOME`
  - explicit profile / wrapper usage
  - fresh validation group per scenario
  - baseline-clear sync before seeding
- A native fully local evaluation path now exists in `compair_cloud/scripts/local_finding_universe_metrics_native.sh`.
- The lane generator now exposes `phase4-native` for `core-local`, so the native local path can be scored without replaying GPT generation packets.
- The native local scorecard path now excludes dropped notification events, which keeps the native row aligned with what the product actually chose to surface.
- The finding-universe evaluator now supports carrying a zero-external-cost note for the native local row.
- The lane generator can now bootstrap a replay-only `core-local-openai-generation` lane that starts at phase 2 and reuses the saved `core-local` retrieval run.
- The replay-only `core-local-openai-generation` lane has now completed and gives us a credible no-outsourced-embeddings self-hosted row for the final profile matrix.
- The reference-profile tuning packager is compatible with the Core venv Python version again.
- The local Core benchmark harness now:
  - waits for API readiness
  - syncs current local source into the container
  - exports `cloud_chunks.sqlite` into the run directory for follow-on evaluation and tuning
- Release automation now has a safer split between:
  - tag-triggered real publishes in `.github/workflows/release.yml`
  - manual no-publish packaging rehearsals in `.github/workflows/release-dry-run.yml`
- The release dry-run workflow now verifies the generated `dist/` artifacts before uploading them, which gives us earlier signal on broken archives or package contents.
- The refreshed published Core image has now passed:
  - raw `/health` and `/capabilities` smoke validation
  - CLI-managed `compair core doctor` validation on `generation=openai`, `embedding=local`
  - `compair demo --mode local` on the recommended self-hosted quality lane
- A tagged CLI release has already exercised the full packaging path successfully once during this cycle, and the expected release artifacts were present.

## Remaining Release Work

### 1. Final Quality Decision

- [x] Record the final native `core-local` result in the launch recommendation.
- [x] Run the `core-local-openai-generation` replay lane (`phase2` + `phase4`) so the final self-hosted profile family is represented in the strict comparison table.
- [x] Freeze the public support matrix and copy it into the remaining launch-facing docs:
  - launch-tier: Cloud
  - strong supported path: Core with OpenAI embeddings + OpenAI generation
  - supported or secondary path: Core with local embeddings + OpenAI generation
  - experimental / manual-review assist: fully local Core
- [x] Decide that the fully local native row should not block launch-tier messaging for the stronger lanes.

### 2. Publish The Validated Runtime

- [x] Refresh the published Core runtime / image so it includes the source fixes validated during this cycle.
- [x] Smoke-test the refreshed published Core image on both:
  - raw container endpoints (`/health`, `/capabilities`)
  - CLI-managed local flow with OpenAI generation + local embeddings
- [x] Package the validated Cloud reranker runtime into `compair_cloud/src/compair_cloud/reranker/`.
- [x] Commit the remaining validated reference profile JSON under `compair_cloud/src/compair_cloud/reference_profiles/`.
- [ ] Confirm the intended default generation lane and profile priority before public rollout.

Notes:

- We already proved that local source and the previously published Core image were not perfectly aligned. Public artifacts need to catch up before launch.
- Use `compair_cloud/docs/core_runtime_refresh_plan.md` as the working publish plan for the Core image refresh.

### 3. Docs And Product Positioning

- [x] Publish one canonical profile-comparison table that includes:
  - Cloud flagship
  - Core with OpenAI embeddings + OpenAI generation
  - Core with local embeddings + OpenAI generation
  - fully local Core native path (clearly marked experimental)
- [x] Update README / quickstart-facing docs so they honestly position:
  - Cloud as the strongest default experience
  - Core + your own OpenAI key as the stronger self-hosted path
  - Core + local embeddings + OpenAI generation as the lower-outsourced-cost self-hosted path
  - fully local Core as the zero-external-cost option, with quality caveats and manual-review framing
- [x] Archive the missing production Scenario C artifact bundle so the seeded validation paper trail is complete.
- [x] Add one short “how we evaluated quality” doc or section pointing to the finding-universe workflow and seeded launch validation.

### 4. Release Automation And External Setup

- [x] Verify the external repos and secrets listed in [ci_release.md](ci_release.md) and [release_checklist.md](release_checklist.md) are actually configured:
  - `RocketResearch-Inc/homebrew-tap`
  - `RocketResearch-Inc/winget-pkgs`
  - `RocketResearch-Inc/compair-packages`
  - package signing secrets
  - release automation tokens
- [x] Exercise the CLI packaging pipeline end to end with either:
  - the `Release Dry Run` workflow, or
  - a tagged CLI release
- [ ] Validate the live install/update paths from the current successful CLI release:
  - GitHub archives
  - Homebrew publication
  - Linux package publication
- [ ] Separately validate the publish paths that the dry-run still cannot exercise:
  - Linux repo publishing
  - WinGet manifest generation / PR path

Needs:

- validation that the current tagged CLI release reached the intended package-manager channels
- a final decision on when to enable the WinGet publish path

Current status:

- External repos and secrets were checked. `WINGET_PUBLISH_ENABLED` is still intentionally unset until we are ready to deal with the WinGet publish/approval path.
- Homebrew publication and upgrade behavior have now been validated on macOS for the current tagged CLI release.
- Linux package publication and direct GitHub-archive install validation are still pending.
- The current tagged CLI release predates two small source-only fixes from this pass: waiting for local Core readiness in `compair core up`, and suppressing `.compair/*` / `~/.compair/credentials.json` noise in report “Compared Files”. Cut a follow-up CLI release if you want those improvements in the shipped binary.

### 5. Runtime Defaults And Debug Hygiene

- [x] Review trace and debug defaults before public release.
- [x] Make sure investigation-only tracing stays opt-in for normal installs and local demos.
- [ ] Confirm the intended notification scoring and reference profile defaults are explicit in docs and deployment config.

Current status:

- `compair_cloud/docker-compose.local.yml` now leaves `COMPAIR_REFERENCE_TRACE` and `COMPAIR_REFERENCE_SOURCE_TRACE` off by default for normal local Cloud runs.
- The evaluation harnesses that depend on tracing still opt in explicitly via their wrapper scripts.
- `COMPAIR_NOTIFICATION_SCORING_TRACE` / `NOTIFICATION_SCORING_TRACE` should remain off unless intentionally debugging.

### 6. Release-Candidate Validation

- [ ] Run the full pre-release CLI checklist from [release_checklist.md](release_checklist.md).
- [ ] Run the seeded launch-validation suite again against the actual release-candidate build if the runtime or defaults changed materially.
- [x] Smoke-test Cloud and Core demos from a clean machine / clean `HOME`.

## What We Can Keep Doing Now

These are good parallel tasks now that the profile matrix is substantially complete:

- wire the final profile messaging into the remaining setup-facing docs
- verify external release repos and secrets
- validate live package-manager channels from the current CLI release
- draft release notes structure and launch messaging
- tighten the support matrix and default recommendations

## What Still Needs Human Input

- Whether fully local should be marketed only as experimental / manual-review assist, or more broadly as a supported privacy-first path.
- Whether we want to invest in another constrained tuning pass now that the native-local result has landed.
- Confirmation that the package-manager repos, release secrets, and signing material are actually in place.
- A final go / no-go decision on public launch once the runtime refresh and external publish-path validation are complete.
