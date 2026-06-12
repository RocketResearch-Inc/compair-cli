# Compair Sync Report

Generated: 2026-06-12T10:42:14-04:00

> Offline sample: this is a prebaked report from `compair demo --offline`. It uses the same disposable demo repos and intentional client/API drift as the live demo, but it does not contact Compair Cloud, start Docker, or call a model API.

## Summary

- 1 notification across 1 document.
- Notification mix: Potential Conflict x1.
- Severity mix: HIGH x1.
- Offline mode: prebaked finding, no hosted account, Docker runtime, or model API required.
- Demo workspace: `/tmp/compair-demo-4133620872`
- Compared repos: `demo-api`, `demo-client`

## Repo: `git@demo.local:compair/demo-client.git`

### Changes

~~~text
Introduce demo client contract drift
README.md           | 4 ++--
src/renderReview.ts | 2 +-
src/reviewClient.ts | 8 ++++----
src/reviewFeed.ts   | 2 +-
4 files changed, 8 insertions(+), 8 deletions(-)
~~~

## Document: `demo.local:compair/demo-client`

### Potential Conflict 1

Type: Potential Conflict Severity: HIGH Delivery: digest

### Notification Rationale

- The target says the client now expects `items[]` with `priority` / `type`, while the peer API docs say `/reviews` returns `reviews[]` with `severity` / `category` / `rationale`.
- The client diff also changes code to read `payload.items` and normalize `payload.priority` / `payload.type`, which directly conflicts with the backend contract and can cause fallback rendering.
- This is likely user-visible because valid `/reviews` responses would be misread by the client, producing silent fallback values or wrong labels.

### Compared Files

- `src/renderReview.ts`
- `src/reviewClient.ts`
- `README.md`
- `src/reviewFeed.ts`
- `api/openapi.yaml`

### Target Evidence

~~~typescript
const payload = await response.json();
return (payload.items ?? []).map((item: any) => renderReviewCard(item));
~~~

~~~typescript
export type Review = {
  priority: "high" | "medium" | "low";
  type: string;
  rationale: string;
};
~~~

### Peer Evidence

The `/reviews` endpoint returns `reviews[]` objects with the fields `severity`, `category`, and `rationale`. Clients should not expect `items`, `priority`, or `type`.

### Context

~~~text
Introduce demo client contract drift
README.md           | 4 ++--
src/renderReview.ts | 2 +-
...
+  priority: "high" | "medium" | "low";
+  type: string;
   rationale: string;
};
~~~

### Reference Excerpts

Source: `demo.local:compair/demo-client` (`src/reviewFeed.ts`)

~~~diff
 const response = await fetch("/reviews");
 const payload = await response.json();
-return (payload.reviews ?? []).map((item: any) => renderReviewCard(item));
+return (payload.items ?? []).map((item: any) => renderReviewCard(item));
~~~

Source: `demo.local:compair/demo-api` (`README.md`)

~~~markdown
This repo defines the backend review contract.
The /reviews endpoint returns reviews[] objects with the fields "severity", "category", and "rationale".
Clients should not expect "items", "priority", or "type".
~~~

Source: `demo.local:compair/demo-api` (`api/openapi.yaml`)

~~~yaml
paths:
  /reviews:
    get:
      responses:
        '200':
          content:
            application/json:
              schema:
                properties:
                  reviews:
                    type: array
                    items:
                      required: [severity, category, rationale]
~~~

### Feedback

The demo client has drifted off the backend contract in three places at once: `src/reviewFeed.ts` now reads `payload.items` instead of the API's documented `reviews`, and `src/reviewClient.ts` / `src/renderReview.ts` switched the review fields from `severity` / `category` to `priority` / `type`. Against `demo-api`'s README and `api/openapi.yaml`, that means a valid `/reviews` response will normalize to fallback values and then render the wrong labels, even though the endpoint shape is unchanged. The README note confirms this is not just docs drift but an actual silent compatibility break in the client path; verify whether the API contract is supposed to change, or restore the client to `reviews`, `severity`, and `category`.

### Why This Matters

This is the kind of drift Compair is designed to catch: one repo looks reasonable on its own, but another repo still owns the contract it depends on.

### References

- `demo.local:compair/demo-api` (2 excerpts)
- `demo.local:compair/demo-client` (1 excerpt)

## Try The Live Demo

Run one of these when you want Compair to generate fresh feedback instead of reading the prebaked sample:

~~~bash
compair demo --mode local
compair demo --mode cloud
~~~
