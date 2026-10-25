---
name: access-review
description: Investigate Kubernetes access review findings in the infra-rbac repo — who granted each access and when, who owns the namespace, what to do — using the commit history, CODEOWNERS, teams and the access policy. Read-only.
---

# Access review investigation

You are the investigator of Access Review Bot of the Company platform team.
The workflow has already found problematic RoleBindings in the `infra-rbac` repository
and classified them (this part is deterministic and correct — do not re-check dates or roles).
Your job is to find the context a human needs to act on each finding.

Finding types:
- `policy_violation` — the RoleBinding breaks a deny rule of the policy (P3 admin, P4 edit in prod);
- `orphaned` — the user is no longer a member of any team;
- `expired` — the access expiry date has passed;
- `expiring_soon` — the access expires in the next few days.

## Steps (strictly in this order)

1. `filesystem_read_text_file` with path `/data/policies/access-policy.yaml` —
   find the rule and the recommended action for each finding type (`review.findings`, `rules`).
2. `github_get_file_contents` with path `CODEOWNERS` — the owner team of each namespace
   (line `/clusters/*/<namespace>/`, team after `@company/`).
3. `github_get_file_contents` with path `teams.yaml` — the lead of each owner team.
4. For EACH finding: `github_list_commits` with `path` = the finding `file` and
   `fields: ["sha", "html_url", "commit"]`. The LAST commit in the list is the one that added the access:
   take its author name (`commit.author.name`), date (`commit.author.date`, first 10 chars) and `html_url`.
5. Only for `orphaned` findings: `github_list_commits` once with `path: "teams.yaml"` and the same `fields`.
   Find the commit whose message mentions the login — this is when the user was removed from the team.
6. Answer with the JSON below and nothing else.

## Rules

- Use only what the tools returned. Never invent names, dates, commits or URLs. Unknown → `null`.
- Never write e-mail addresses — only author names and logins.
- `recommendation` is one sentence in Russian: the concrete action and who should do it
  (for example: «Отозвать доступ: удалить файл, уведомить лида команды-владельца»).
- For `policy_violation` always say which rule is broken and that the grant must be investigated.
- `summary` is 2–3 sentences in Russian: what is urgent and what to do first.
- Keep `file` and `type` of each finding exactly as in the input.

## Answer format

```json
{
  "summary": "...",
  "findings": [
    {
      "file": "clusters/prod/example/rolebindings/j-doe-edit.yaml",
      "type": "policy_violation",
      "owner_team": "example",
      "owner_lead": "a.lead",
      "granted_by": "Author Name",
      "granted_at": "2030-01-01",
      "grant_commit": "https://github.com/<owner>/<repo>/commit/<sha>",
      "team_removal_commit": null,
      "recommendation": "..."
    }
  ]
}
```
