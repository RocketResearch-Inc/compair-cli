# How We Evaluated Quality

This is the short version of how the current Compair launch recommendations were derived.

We used two complementary evaluation styles because they answer different product questions.

## 1. Seeded Launch Validation

This is the practical “will a real user see something useful?” check.

We seed a small, known cross-repo drift into realistic repos, run Compair end to end, and verify whether the system surfaces meaningful feedback or notifications.

What it tells us:

- whether the product works end to end in a realistic workflow
- whether the surfaced feedback is good enough for demos and launch confidence
- whether install/demo/report/notification flows behave correctly in practice

Current reference:

- [launch_validation.md](launch_validation.md)

## 2. Finding-Universe Benchmark

This is the stricter comparison benchmark.

We run Compair and a direct LLM baseline on the same frozen workspace, cluster the judged findings into canonical issues, and score:

- precision
- recall
- F1
- surfaced / abstained counts
- cost

What it tells us:

- which profile families produce the strongest generative findings
- how Cloud and self-hosted profiles compare directionally
- whether a lane is good enough to market as a review-quality path

Current references:

- `compair_cloud/scripts/local_finding_universe_metrics_production_final.sh`
- `compair_cloud/scripts/local_finding_universe_metrics_native.sh`
- `compair_cloud/scripts/evaluate_finding_universe.py`
- `compair_cloud/docs/reference_profile_playbook.md`
- `compair_cloud/docs/reference_profile_results_matrix.md`

## Why We Use Both

The seeded launch suite is closer to product reality, but it is small.

The finding-universe benchmark is broader and stricter, but it is still a benchmark rather than a complete simulation of every user workflow.

Taken together, they let us make a more honest launch recommendation:

- seeded validation tells us whether the product works in realistic review flows
- finding-universe scoring tells us which profile families are strongest and which ones should be positioned more cautiously

## How To Read The Current Profile Story

- Cloud is the strongest out-of-the-box path because it has both strong seeded validation and the strongest overall generative story.
- Core with OpenAI embeddings + OpenAI generation is the strongest self-hosted quality path.
- Core with local embeddings + OpenAI generation is the strongest lower-outsourced-cost self-hosted path.
- Fully local Core currently has more value as a retrieval-assist/manual-review path than as a launch-tier generative finding lane.

## Important Caveat

Not every profile result came from the exact same judged universe.

That means some cross-profile comparisons are directional rather than perfectly apples-to-apples. When we need a tighter story, we prefer:

- the canonical results matrix for positioning
- the seeded launch suite for launch confidence

Current summary pages:

- [launch_readiness.md](launch_readiness.md)
- `compair_cloud/docs/reference_profile_results_matrix.md`
