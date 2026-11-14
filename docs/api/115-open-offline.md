# 115 Open offline download API

The `115 Open` tool keeps the original `info_hash` returned by 115, together
with the storage and account that submitted the task. The receipt is persisted
even when general download task persistence is disabled. Treat the hash as an
opaque string; it is not necessarily a BTIH or an ed2k file hash.

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
