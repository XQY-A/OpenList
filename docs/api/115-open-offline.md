# 115 Open offline download API

The `115 Open` tool keeps the original `info_hash` returned by 115, together
with the storage and account that submitted the task. The receipt is persisted
even when general download task persistence is disabled. Treat the hash as an
opaque string; it is not necessarily a BTIH or an ed2k file hash.

See the [usage and upgrade guide](../usage/115-open-offline.md) for the management
interface, transfer policies and deployment steps, and the
[personal build guide](../development/custom-115-build.md) for paired frontend
and backend builds.

These endpoints use the usual OpenList authentication and response envelope.
Check the JSON `code`: a business error is still returned with HTTP 200.
Only the task creator and administrators can query a task. Other users receive
the same 404 response as an unknown task ID.

## Add downloads

`POST /api/fs/add_offline_download`

```json
{
  "urls": ["<download URI>", "<another download URI>"],
  "path": "/115/Downloads",
  "tool": "115 Open"
}
```

Adding downloads remains asynchronous. A successful item means its local task
has been saved and queued, not that 115 has already accepted the download.
The response keeps the existing `data.tasks` array and adds `data.results` for
requests that explicitly select `115 Open`:

```json
{
  "code": 200,
  "message": "success",
  "data": {
    "tasks": [
      {
        "id": "<local task ID>",
        "tool": "115 Open",
        "info_hash": "",
        "submission_state": "pending"
      }
    ],
    "results": [
      {"index": 0, "task_id": "<local task ID>"},
      {"index": 1, "error": "<reason the second task could not be created>"}
    ]
  }
}
```

The example omits unchanged task fields. Each result's zero-based `index`
refers to the original `urls` array, including empty or whitespace-only items.
All successfully created tasks remain in `tasks`, even if another item fails.
At least one created task yields code 200; inspect every result for partial
failure. An empty batch or a batch containing only blank items yields code 400.
If every nonblank item fails to create, code 500 includes the per-item results.
Request-wide permission, path or decoding errors retain the usual error format.
Other tools retain their existing batch response and behavior.

Retry only the failed inputs after addressing their errors. Do not resubmit
successful items to obtain their hash.

## Read the receipt

Use the returned local task ID with any of these endpoints:

| Endpoint | Result |
| --- | --- |
| `POST /api/task/offline_download/info?tid=<local task ID>` | One task |
| `GET /api/task/offline_download/undone` | Tasks that have not ended |
| `GET /api/task/offline_download/done` | Ended tasks, including restored tasks |

Every 115 Open task has these additional fields. Other task types may omit them.

| Field | Meaning |
| --- | --- |
| `tool` | `115 Open` |
| `info_hash` | The exact cloud receipt, or an empty string while unavailable |
| `submission_state` | Submission outcome, separate from download execution `state` |

The submission states are:

| Value | Meaning |
| --- | --- |
| `pending` | Saved locally; no submission has started |
| `submitting` | Submission intent and account binding were saved; the outcome is pending |
| `accepted` | A cloud receipt is known; subsequent execution queries that same receipt |
| `failed` | Preparation failed or the request was authoritatively rejected |
| `unknown` | The request may have been accepted, but no authoritative receipt is available |
| `legacy` | An older task lacks a reliable saved receipt or account binding |

The first add response may already contain a hash if acceptance was fast.
Otherwise, query the same task ID until the state changes. A later task response
contains `"submission_state": "accepted"` and the original, nonempty `info_hash`.
An accepted receipt does not by itself mean the download completed; continue
to use the existing execution state, progress and error fields.

Restarts and explicit retries preserve a known receipt and query the original
cloud task instead of submitting it again. A receipt whose database write failed
remains visible in memory with a persistence error; retrying first attempts to
save it. A crash before that write succeeds cannot recover the receipt, so the
last durable `submitting` state becomes `unknown`. Tasks with `unknown` or
`legacy` state are not automatically resubmitted and cannot bypass this rule
through the retry endpoints. Verify the cloud account before intentionally
creating a new download.

## Optionally remove the cloud download record

The existing deletion endpoints accept `delete_cloud_record=true`:

| Endpoint | Targets |
| --- | --- |
| `POST /api/task/offline_download/delete?tid=<ID>&delete_cloud_record=true` | One task |
| `POST /api/task/offline_download/delete_some?delete_cloud_record=true` | JSON array of task IDs |
| `POST /api/task/offline_download/clear_done?delete_cloud_record=true` | All ended tasks accessible to the caller |
| `POST /api/task/offline_download/clear_succeeded?delete_cloud_record=true` | All successful tasks accessible to the caller |

Omitting the option, or passing `false`, preserves the existing local-only
deletion and response format. Local deletion does not cancel a running download.
The saved deletion marker prevents it from reappearing after a restart. If that
marker cannot be saved, deletion reports an error and retains the task.

The option must occur once with the literal value `true` or `false`. Empty,
duplicate, malformed or other values return code 400 without deleting anything.
It is separate from `delete_policy`, which controls the existing transfer and
temporary-file behavior.

When `true`, the operation applies only to `115 Open` download tasks. Other
tools, `115 Cloud`, and `offline_download_transfer` tasks return a conflict and
remain in the list. A task must have a reliable cloud receipt and its original
storage/account binding, and its local state must be succeeded, failed or
canceled. Use the existing cancel action for an active local task.

Before cloud deletion, the server checks the current bound account and queries
the cloud task. Only cloud status `2` (completed), `-1` (failed), or a reliable
complete lookup proving absence is accepted. A currently running cloud task,
unknown status, account change or query failure leaves the local task intact.
Cloud requests and waits for task/account coordination are bounded by a timeout.

Cloud deletion always uses `del_source_file=0`: it removes the 115 download
record and **preserves downloaded files**. There is no new delete-files option.
Existing transfer policies still apply independently to their files.

The server saves cloud deletion intent before the first lookup, confirms and
saves the cloud result, then saves the local deletion marker and removes the
list entry. Saving intent before lookup also protects a confirmed absence whose
later database write fails. A first attempt that proves no deletion was sent
can reset its intent after a failed query or an ineligible cloud state, provided
that reset is saved successfully.

An uncertain deletion remains queryable. Explicitly retrying `deleting` or
`unknown` cleanup only checks the cloud record: reliable absence completes local
cleanup, while any existing record returns code 409 with reason
`cloud_delete_unknown` without sending another delete. The API cannot determine
whether that hash belongs to the original record or a newly created download.
When a confirmed `deleted` record appears again, the reason is
`cloud_record_reappeared`. These protections also apply after a database failure
and process restart. Inspect the original cloud account before further action;
local-only deletion is still available.

The 115 API has no task generation or conditional-delete parameter. Coordination
covers this OpenList process; another client can recreate the same hash between
the cloud query and deletion. This is not an atomic check-and-delete across
clients.

### Deletion results

A single `true` request returns one result in `data`. A batch or clear request
returns `data.results`, with the same structure for every target:

```json
{
  "code": 200,
  "message": "success",
  "data": {
    "results": [
      {
        "task_id": "<removed task ID>",
        "local_deleted": true,
        "cloud_state": "deleted",
        "code": 200
      },
      {
        "task_id": "<retained task ID>",
        "local_deleted": false,
        "cloud_state": "none",
        "code": 409,
        "reason": "cloud_task_active",
        "error": "<explanation>"
      }
    ]
  }
}
```

Batch and clear responses use top-level code 200 even when some or all targets
fail; inspect each result's `code` and `local_deleted`. Single-item errors use
the same error code at the top level and in the result. Permission failures and
unknown task IDs both produce 404; state, type and binding conflicts use 409;
cloud request or persistence failures use 500. `reason` is a machine-readable
error category when available, and `error` is filtered using the existing
privacy rules. Duplicate IDs within a `true` batch reuse their first result
without sending another delete request. Failure never silently falls back to
local-only deletion.

`cloud_state` records the last known cleanup outcome:

| Value | Meaning |
| --- | --- |
| `none` | No cloud deletion has been sent |
| `deleting` | Deletion intent has been saved |
| `unknown` | The previous attempt is uncertain; retries only reconcile confirmed absence |
| `deleted` | Deletion or absence was confirmed; local cleanup may still need to finish |

A failed local write can therefore return `cloud_state: "deleted"` together
with `local_deleted: false`. Keep that task ID and retry its cleanup rather than
creating another download. On restart, interrupted `deleting` becomes `unknown`;
cleanup is never automatically sent during recovery.

### Advertised availability

115 Open task responses also include:

| Field | Meaning |
| --- | --- |
| `can_delete_cloud_record` | Whether the local task and current storage binding allow a cloud-cleanup attempt |
| `cloud_delete_unavailable_reason` | An availability reason code, empty when an attempt is allowed |
| `cloud_delete_state` | The saved or currently known cleanup state |

Computing availability does not query 115. A task may advertise availability
while its cloud counterpart is still active; the deletion request checks again
and returns a conflict. Clients must tolerate missing fields when connected to
an older backend, and should keep the cloud-cleanup option unchecked by default.
