# Comment moderation detector (report-only)

`moderate_comment.py` scores issue / PR / discussion content for the malware-
attachment spam campaign (LLM-written "here's a fix" comments with a
`*_fix.zip` attached via GitHub's `user-attachments` uploader). The workflow
`.github/workflows/comment-moderation.yml` runs it on every new/edited comment
and body.

## It is report-only

The detector **takes no action** — it prints the tier it *would* assign (A/B/C)
plus the matched signals to the run log and the job summary. The workflow token
is `contents: read`; there is no minimize/delete/label/lock/block anywhere. The
point is to bake against real traffic and measure the false-positive rate before
enforcement is ever wired up.

## Tiers (what they *would* mean once enforced)

- **A** — hard payload from a fresh, third-party account with a payload-specific
  corroborator (bait filename, throwaway host, masquerade double-extension).
  Future enforce action: minimize + maintainer alert (delete/block human-gated).
- **B** — suspicious; future action: minimize + flag for review (reversible).
- **C** — log only.
- **SKIP** — bot/app author.

Two corrections from adversarial review are baked in:
1. github.com is not blanket-trusted — `/raw/` and `/blob/`, `user-attachments`
   `files/` and `assets/`, gist archives, foreign `releases/download`, and
   `objects.githubusercontent.com` (where downloads redirect) are all
   extension-gated, so hosting the zip on a throwaway repo — or behind a URL
   decoration like `?x=1`, a trailing slash, a protocol-relative `//host`, or a
   trailing-dot FQDN — doesn't evade it.
2. Timing and "third-party" never by themselves justify Tier A — a payload-
   specific signal is required, so an eager helper's `repro.zip` can't be
   auto-actioned.

## Run the self-test

```bash
python3 .github/scripts/moderate_comment.py --selftest
```

Checks both real malware samples (→ A), the evasion variants (→ A: percent-
encoded dot, masquerade double-extension, raw/blob hosting, `assets/` and
object-store archives, and query/slash/protocol-relative/trailing-dot
decorations), the legitimate patterns (contributor `.tar.gz`, screenshots,
logs, release links, `/tree/` nav, self-issue `repro.zip`, bots) — none of
which reach the destructive tier — plus a set of adversarial 65 536-char bodies
that must score in well under the run budget (ReDoS guard).

## Graduating out of report-only (deliberate, staged)

1. **Bake** in report-only for a few weeks. Read the job summaries; confirm no
   legitimate comment is scored A, and few/none are scored B.
2. **Minimize-only:** add `issues: write` / `pull-requests: write` /
   `discussions: write`, and have the script's caller minimize (GraphQL
   `minimizeComment(classifier: SPAM)`) on tier A/B. Still reversible; still no
   delete/block. Add an auto-unminimize path for edited-out URLs.
3. **Human-gated escalation:** maintainer reaction triggers delete + org-block
   (the block needs a separate org-admin credential — do NOT put that token in
   this public repo).

Do not skip step 1. The workflow only runs from the default branch, so it is
untestable in a PR — the self-test is your pre-merge check.
