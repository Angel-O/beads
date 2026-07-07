#!/usr/bin/env python3
"""Report-only malware-attachment detector for GitHub issue/PR/discussion content.

This script NEVER takes a moderation action. It reads one event's content from
environment variables, computes the response tier it WOULD assign under the
policy, and prints a human summary + one JSON line. The workflow writes that to
the run log and the job summary. Graduating it to real enforcement is a separate,
deliberate change (see README).

Inputs (all via env; the workflow injects them, never interpolated into shell):
  BODY                 raw markdown of the comment / issue / PR / discussion body
  AUTHOR_LOGIN         commenter login
  AUTHOR_ASSOCIATION   NONE / FIRST_TIME_CONTRIBUTOR / CONTRIBUTOR / MEMBER / ...
  AUTHOR_TYPE          "User" or "Bot"
  THREAD_AUTHOR_LOGIN  login of the issue/PR/discussion author (may be empty)
  THREAD_CREATED_AT    ISO8601 of the thread creation (may be empty)
  EVENT_CREATED_AT     ISO8601 of the comment/body (may be empty)
  EVENT_NAME           github event name (issue_comment, issues, ...)
  ACCOUNT_CREATED_AT   ISO8601 of the author account creation (may be empty)
  ACCOUNT_REPOS        author public_repos count (may be empty)
  ACCOUNT_FOLLOWERS    author followers count (may be empty)

Run `moderate_comment.py --selftest` to check the logic against the real
malicious samples and the known-legitimate patterns.
"""
import base64
import datetime as dt
import html
import json
import os
import re
import sys
import unicodedata
import urllib.parse

OWN_ORG = "gastownhall"

TRUSTED_FULL = {"OWNER", "MEMBER", "COLLABORATOR"}   # hard-exempt -> Tier C
DOWNGRADE = {"CONTRIBUTOR"}                            # capped at Tier B
# everything else (NONE, FIRST_TIMER, FIRST_TIME_CONTRIBUTOR, MANNEQUIN, "") is untrusted

ARCHIVE = {"zip", "rar", "7z", "tar", "gz", "tgz", "xz", "txz", "bz2", "tbz2",
           "zst", "cab", "ace", "arj", "z", "lzh"}
EXEC = {"exe", "msi", "msix", "appx", "scr", "com", "pif", "bat", "cmd", "ps1",
        "psm1", "vbs", "vbe", "wsf", "wsh", "hta", "jar", "apk", "dmg", "pkg",
        "lnk", "iso", "img", "vhd", "vhdx", "cpl", "msc", "reg", "sct"}
BLOCK_EXT = ARCHIVE | EXEC
SUSP_EXT = {"html", "htm", "svg", "chm", "one", "docm", "xlsm", "pptm", "docx",
            "xlsx", "pptx", "pdf", "js", "jse", "sh", "run", "bin", "deb", "rpm"}
ALLOW_EXT = {"png", "jpg", "jpeg", "gif", "webp", "mp4", "mov", "md", "txt",
             "log", "patch", "diff", "json", "jsonl", "csv", "yaml", "yml",
             "toml", "go", "rs", "py", "sql", "out", "trace"}

SHORTENERS = {"bit.ly", "tinyurl.com", "t.co", "is.gd", "rb.gy", "goo.gl",
              "cutt.ly", "shorturl.at", "tiny.cc", "ow.ly", "buff.ly",
              "rebrand.ly", "s.id", "v.gd", "soo.gd", "shorte.st"}
FILEHOSTS = {"mediafire.com", "anonfiles.com", "anonfile.com", "gofile.io",
             "transfer.sh", "file.io", "ufile.io", "pixeldrain.com",
             "krakenfiles.com", "uploadnow.io", "filebin.net", "catbox.moe",
             "litterbox.catbox.moe", "easyupload.io", "bayfiles.com",
             "dosya.co", "mega.nz", "telegra.ph", "t.me"}
GH_MEDIA = {"camo.githubusercontent.com", "avatars.githubusercontent.com"}
NAV_PREFIXES = ("/tree/", "/commit/", "/commits/", "/compare/", "/issues",
                "/pull/", "/pulls", "/discussions", "/wiki", "/actions",
                "/releases/tag/", "/releases/latest", "/blame/", "/graphs/",
                "/labels", "/milestones", "/projects", "/security")

BAIT_NAME = re.compile(
    r"(?i)(^|[\W_])(fix|patch|solution|update|crack|keygen|setup|install(er)?)"
    r"([\W_]?(v?\d+|win(32|64)?|x(86|64)|mac|osx|linux))*"
    r"\.(zip|rar|7z|exe|msi|bat|scr)$")
MASQUERADE = re.compile(r"\.(pdf|docx?|txt|log|md|jpe?g|png)\.(zip|exe|scr|msi|7z|rar)$", re.I)
STYLO = [re.compile(p, re.I) for p in (
    r"^\s*(man|oh man|ugh|wow|yeah)[, ]",
    r"\bran into the same (thing|issue|problem)\b",
    r"\bI ended up \w+ing\b")]

MD_LINK = re.compile(r"\[(?P<text>[^\]]*)\]\(\s*<?(?P<url>[^)\s<>]+)>?(?:\s+[\"'(][^)\"']*[\"')])?\s*\)")
AUTOLINK = re.compile(r"<(?P<url>https?://[^>\s]+)>")
HTML_URL = re.compile(r"<(?:a|img|source|video|iframe|form)\b[^>]*?\b(?:href|src|action)\s*=\s*"
                      r"(?:\"(?P<u1>[^\"]*)\"|'(?P<u2>[^']*)'|(?P<u3>[^\s>]+))", re.I)
REF_DEF = re.compile(r"^[ ]{0,3}\[(?P<label>[^\]]+)\]:\s*<?(?P<url>\S+?)>?(?:\s+[\"'(].*)?$", re.M)
RAW_URL = re.compile(r"(?<![\(<\"'])\b(?:https?://)[^\s<>\"'\)\]]+")
ZERO_WIDTH = re.compile("[​‌‍﻿]")


def normalize_url(u):
    u = unicodedata.normalize("NFKC", u)
    for _ in range(3):
        d = urllib.parse.unquote(u)
        if d == u:
            break
        u = d
    u = u.rstrip(".,;:!?\"'>]}")
    while u.endswith(")") and u.count("(") < u.count(")"):
        u = u[:-1]
    return u


def code_regions(body):
    regions = []
    for m in re.finditer(r"(`{3,}|~{3,})[\s\S]*?\1", body):
        regions.append((m.start(), m.end()))
    for m in re.finditer(r"(`+)(?:[^`]|[^`][\s\S]*?[^`])\1(?!`)", body):
        regions.append((m.start(), m.end()))
    return regions


def in_code(off, regions):
    return any(a <= off < b for a, b in regions)


def extract_urls(body):
    body = unicodedata.normalize("NFKC", body)
    body = ZERO_WIDTH.sub("", body)
    body = html.unescape(body)
    regions = code_regions(body)
    first_nl = body.find("\n")
    first_line_end = len(body) if first_nl < 0 else first_nl
    urls = []

    def add(raw, off, text=""):
        if not raw or raw.startswith("#") or raw.startswith("mailto:"):
            return
        urls.append({
            "url": normalize_url(raw),
            "text": text or "",
            "in_code": in_code(off, regions),
            "first_line": off <= first_line_end,
        })

    for m in MD_LINK.finditer(body):
        add(m.group("url"), m.start(), m.group("text"))
    for m in AUTOLINK.finditer(body):
        add(m.group("url"), m.start())
    for m in HTML_URL.finditer(body):
        add(m.group("u1") or m.group("u2") or m.group("u3"), m.start())
    refs = {m.group("label").lower(): (m.group("url"), m.start()) for m in REF_DEF.finditer(body)}
    for label, (u, off) in refs.items():
        if re.search(r"\[[^\]]*\]\[%s\]|\[%s\](?!\()" % (re.escape(label), re.escape(label)), body, re.I):
            add(u, off, label)
    for m in RAW_URL.finditer(body):
        add(m.group(0), m.start())
    # dedupe by (url, in_code)
    seen, out = set(), []
    for u in urls:
        k = (u["url"], u["in_code"])
        if k not in seen:
            seen.add(k)
            out.append(u)
    return out


def ext_of(path):
    base = urllib.parse.unquote(path).rsplit("/", 1)[-1].lower().rstrip(".,;:!?\"')]}")
    for comp in ("tar.gz", "tar.xz", "tar.bz2"):
        if base.endswith("." + comp):
            return comp.split(".")[-1]
    return base.rsplit(".", 1)[-1] if "." in base else ""


def classify(u):
    """Return (cls, score, flag_host). cls in {ALLOW, CORE, SUSP}."""
    raw = u["url"]
    parsed = urllib.parse.urlparse(raw if "://" in raw else "https://" + raw)
    netloc = parsed.netloc
    host = (parsed.hostname or "").lower()
    if host.startswith("www."):
        host = host[4:]
    path = parsed.path
    ext = ext_of(parsed.path)

    # look-alike / obfuscated hosts
    if "@" in netloc or host.startswith("xn--") or re.fullmatch(r"[\d.]+", host or "x"):
        return ("SUSP", 3, False)

    if host == "github.com":
        if path.startswith("/user-attachments/assets/"):
            return ("ALLOW", 0, False)
        if path.startswith("/user-attachments/files/"):          # the crux
            if ext in BLOCK_EXT:
                return ("CORE", 5, False)
            if ext in ALLOW_EXT:
                return ("ALLOW", 0, False)
            return ("SUSP", 2, False)
        m = re.match(r"^/([^/]+)/[^/]+/releases/download/", path)
        if m:
            return ("ALLOW", 0, False) if m.group(1).lower() == OWN_ORG else ("SUSP", 1, False)
        # correction #1: raw content on github.com is NOT auto-allowed
        is_raw = path.startswith("/") and ("/raw/" in path or (re.search(r"/blob/", path) and "raw=true" in (parsed.query or "")))
        if is_raw:
            return ("CORE", 5, False) if ext in BLOCK_EXT else ("ALLOW", 0, False)
        if any(path.startswith("/" + p.split("/")[1]) for p in NAV_PREFIXES) or re.match(r"^/[^/]+/[^/]+/blob/", path):
            return ("ALLOW", 0, False)
        # unknown github.com path: allow unless it dangles a block-class file
        return ("SUSP", 2, False) if ext in BLOCK_EXT else ("ALLOW", 0, False)

    if host == "gist.github.com":
        if "/raw/" in path or ext in BLOCK_EXT:
            return ("CORE", 5, False) if ext in BLOCK_EXT else ("ALLOW", 0, False)
        return ("ALLOW", 0, False)
    if host in GH_MEDIA:
        return ("ALLOW", 0, False)
    if host == "objects.githubusercontent.com":
        return ("SUSP", 2, False) if ext in BLOCK_EXT else ("ALLOW", 0, False)
    if host == "raw.githubusercontent.com":
        return ("CORE", 4, False) if ext in BLOCK_EXT else ("ALLOW", 0, False)

    if host in SHORTENERS or host in FILEHOSTS:
        return ("CORE", 5, True)
    if host in ("cdn.discordapp.com", "media.discordapp.net"):
        return ("ALLOW", 0, False) if ext in {"png", "jpg", "jpeg", "gif", "webp"} else ("CORE", 5, True)
    if host.endswith("dropbox.com"):
        return ("CORE", 5, True) if ("dl=1" in (parsed.query or "") or "/scl/" in path) else ("SUSP", 2, False)
    if host == "drive.google.com":
        return ("SUSP", 3, False) if ("export=download" in (parsed.query or "") or path.startswith("/uc")) else ("SUSP", 1, False)

    if ext in EXEC:
        return ("CORE", 4, False)
    if ext in ARCHIVE:
        return ("SUSP", 3, False)
    if ext in SUSP_EXT:
        return ("SUSP", 1, False)
    if re.search(r"[?&][^=]*=[^&]*\.(zip|rar|7z|exe|msi)\b", raw, re.I):
        return ("SUSP", 1, False)
    return ("ALLOW", 0, False)


def _hours_since(iso):
    if not iso:
        return None
    try:
        t = dt.datetime.fromisoformat(iso.replace("Z", "+00:00"))
    except ValueError:
        return None
    now = dt.datetime.now(dt.timezone.utc)
    return (now - t).total_seconds() / 3600.0


def _minutes_between(a, b):
    if not a or not b:
        return None
    try:
        ta = dt.datetime.fromisoformat(a.replace("Z", "+00:00"))
        tb = dt.datetime.fromisoformat(b.replace("Z", "+00:00"))
    except ValueError:
        return None
    return abs((tb - ta).total_seconds()) / 60.0


def evaluate(ev):
    """ev is a dict of the env inputs (+ optional _now_age_h override for tests).
    Returns a verdict dict. REPORT-ONLY: the tier is advisory only."""
    author_type = (ev.get("AUTHOR_TYPE") or "User")
    login = (ev.get("AUTHOR_LOGIN") or "")
    if author_type == "Bot" or login.endswith("[bot]") or login in ("dependabot[bot]", "github-actions[bot]"):
        return {"tier": "SKIP", "reason": "bot/app author", "score": 0, "findings": []}

    assoc = (ev.get("AUTHOR_ASSOCIATION") or "").upper()
    trusted_full = assoc in TRUSTED_FULL
    downgrade = assoc in DOWNGRADE

    body = ev.get("BODY") or ""
    findings = []
    for u in extract_urls(body):
        cls, base, flag = classify(u)
        if cls == "ALLOW":
            continue
        basename = urllib.parse.unquote(u["url"]).rsplit("/", 1)[-1]
        bait = bool(BAIT_NAME.search(basename) or BAIT_NAME.search(u.get("text", "")))
        masq = bool(MASQUERADE.search(basename))
        s = base + 3 * bait + 2 * masq + 2 * u["first_line"] + (-2 if u["in_code"] else 0)
        findings.append({"url": u["url"], "cls": cls, "score": s, "flag_host": flag,
                         "bait": bait, "masq": masq, "in_code": u["in_code"],
                         "first_line": u["first_line"]})
    if not findings:
        return {"tier": "C", "reason": "no risky URL", "score": 0, "findings": []}

    top = max(findings, key=lambda f: f["score"])
    score = top["score"]

    age_h = ev.get("_age_h")
    if age_h is None:
        age_h = _hours_since(ev.get("ACCOUNT_CREATED_AT"))
    if age_h is not None:
        score += 4 if age_h < 24 else 3 if age_h < 168 else 2 if age_h < 720 else 0

    is_body = (ev.get("EVENT_NAME") or "") in ("issues", "pull_request", "pull_request_target", "discussion")
    dt_m = None if is_body else _minutes_between(ev.get("THREAD_CREATED_AT"), ev.get("EVENT_CREATED_AT"))
    if dt_m is not None:
        score += 3 if dt_m < 15 else 1 if dt_m < 60 else 0

    thread_author = ev.get("THREAD_AUTHOR_LOGIN") or ""
    third_party = bool(thread_author) and (login != thread_author)
    score += 2 * third_party

    if sum(bool(p.search(body)) for p in STYLO) >= 2:
        score += 1

    try:
        repos = int(ev.get("ACCOUNT_REPOS") or -1)
        followers = int(ev.get("ACCOUNT_FOLLOWERS") or -1)
        if repos == 0 and followers == 0:
            score += 1
    except ValueError:
        pass

    # correction #1: a code-context CORE is only "hard" if the host itself is a flag host
    hard_core = (top["cls"] == "CORE") and (not top["in_code"] or top["flag_host"])
    # correction #2: corroboration must be payload-specific (NOT timing / third-party)
    corroborated = top["bait"] or top["flag_host"] or top["masq"]

    verdict = {"score": score, "findings": findings, "top": top,
               "third_party": third_party, "age_h": age_h, "dt_m": dt_m,
               "hard_core": hard_core, "corroborated": corroborated}

    if trusted_full:
        verdict["tier"] = "C"
        verdict["reason"] = "trusted author (member/collaborator/owner) — exempt"
        return verdict

    self_issue = bool(thread_author) and (login == thread_author)

    if (hard_core and score >= 9 and corroborated and third_party and not self_issue and not downgrade):
        verdict["tier"] = "A"
        verdict["reason"] = "hard payload + corroborated + fresh third-party account"
    elif score >= 6:
        verdict["tier"] = "B"
        verdict["reason"] = "suspicious — would minimize + flag for review"
    else:
        verdict["tier"] = "C"
        verdict["reason"] = "weak signals — log only"

    if self_issue and verdict["tier"] == "A":
        verdict["tier"] = "B"
        verdict["reason"] = "self-authored thread — capped at review (never auto-destructive)"
    if downgrade and verdict["tier"] == "A":
        verdict["tier"] = "B"
        verdict["reason"] = "contributor — capped at review (not auto-destructive)"
    return verdict


# ----------------------------- self-test -----------------------------

SAMPLES = [
    ("T1 sodesecago #4643 (real malware)", {
        "BODY": "[bd_fix_v1.zip](https://github.com/user-attachments/files/29761409/bd_fix_v1.zip)\n"
                "Man, that silent bulk import is such a headache. I ran into the same thing when my "
                "single briefing file turned into a bunch of junk beads. I ended up swapping the flag "
                "logic in the go source so -f actually maps to the body content instead of the batch parser.",
        "AUTHOR_LOGIN": "sodesecago", "AUTHOR_ASSOCIATION": "NONE", "AUTHOR_TYPE": "User",
        "THREAD_AUTHOR_LOGIN": "seanmartinsmith", "EVENT_NAME": "issue_comment",
        "_age_h": 0.15, "THREAD_CREATED_AT": "2026-07-07T18:51:17Z",
        "EVENT_CREATED_AT": "2026-07-07T18:54:38Z", "ACCOUNT_REPOS": "0", "ACCOUNT_FOLLOWERS": "0",
    }, "A"),
    ("T2 kogakosanu62 #4637 (real malware)", {
        "BODY": "[bd_fix_win.zip](https://github.com/user-attachments/files/29761303/bd_fix_win.zip)\n"
                "Man, that WSL2 loopback thing is a total nightmare. I ran into the same issue. "
                "I ended up rewriting the identity check to use process handles instead of CWD.",
        "AUTHOR_LOGIN": "kogakosanu62", "AUTHOR_ASSOCIATION": "NONE", "AUTHOR_TYPE": "User",
        "THREAD_AUTHOR_LOGIN": "someone", "EVENT_NAME": "issue_comment",
        "_age_h": 0.2, "THREAD_CREATED_AT": "2026-07-07T18:48:00Z",
        "EVENT_CREATED_AT": "2026-07-07T18:51:47Z", "ACCOUNT_REPOS": "0", "ACCOUNT_FOLLOWERS": "0",
    }, "A"),
    ("E1 percent-encoded dot evasion", {
        "BODY": "[fix](https://github.com/user-attachments/files/1/bd_fix_v1%2Ezip)\nheres the fix",
        "AUTHOR_LOGIN": "burner", "AUTHOR_ASSOCIATION": "NONE", "AUTHOR_TYPE": "User",
        "THREAD_AUTHOR_LOGIN": "victim", "EVENT_NAME": "issue_comment", "_age_h": 0.1,
        "THREAD_CREATED_AT": "2026-07-07T00:00:00Z", "EVENT_CREATED_AT": "2026-07-07T00:05:00Z",
    }, "A"),
    ("E2 masquerade double-extension", {
        "BODY": "[report](https://github.com/user-attachments/files/9/report.pdf.zip)",
        "AUTHOR_LOGIN": "burner", "AUTHOR_ASSOCIATION": "NONE", "AUTHOR_TYPE": "User",
        "THREAD_AUTHOR_LOGIN": "victim", "EVENT_NAME": "issue_comment", "_age_h": 0.1,
        "THREAD_CREATED_AT": "2026-07-07T00:00:00Z", "EVENT_CREATED_AT": "2026-07-07T00:05:00Z",
    }, "A"),
    ("E3 raw.githubusercontent hosting (correction #1)", {
        "BODY": "grab it: https://github.com/attacker/beads/raw/main/bd_fix_v1.zip",
        "AUTHOR_LOGIN": "burner", "AUTHOR_ASSOCIATION": "NONE", "AUTHOR_TYPE": "User",
        "THREAD_AUTHOR_LOGIN": "victim", "EVENT_NAME": "issue_comment", "_age_h": 0.1,
        "THREAD_CREATED_AT": "2026-07-07T00:00:00Z", "EVENT_CREATED_AT": "2026-07-07T00:05:00Z",
    }, "A"),
    ("L1 contributor .tar.gz harness (not destructive)", {
        "BODY": "Here's a repro harness [bd-hooks-testbed.tar.gz](https://github.com/user-attachments/files/27/bd-hooks-testbed.tar.gz)",
        "AUTHOR_LOGIN": "pmgledhill102", "AUTHOR_ASSOCIATION": "CONTRIBUTOR", "AUTHOR_TYPE": "User",
        "THREAD_AUTHOR_LOGIN": "maphew", "EVENT_NAME": "issue_comment", "_age_h": 8760,
    }, {"B", "C"}),
    ("L2 screenshot on assets path", {
        "BODY": "see <img src=\"https://github.com/user-attachments/assets/6d9672de-3c35\" />",
        "AUTHOR_LOGIN": "newbie", "AUTHOR_ASSOCIATION": "NONE", "AUTHOR_TYPE": "User",
        "THREAD_AUTHOR_LOGIN": "newbie", "EVENT_NAME": "issue_comment", "_age_h": 0.1,
    }, "C"),
    ("L3 new user attaches crash.log to files path", {
        "BODY": "logs: [crash.log](https://github.com/user-attachments/files/5/crash.log)",
        "AUTHOR_LOGIN": "newbie", "AUTHOR_ASSOCIATION": "NONE", "AUTHOR_TYPE": "User",
        "THREAD_AUTHOR_LOGIN": "newbie", "EVENT_NAME": "issues", "_age_h": 0.1,
    }, "C"),
    ("L4 own-org release zip in code fence", {
        "BODY": "install with:\n```\ncurl -LO https://github.com/gastownhall/beads/releases/download/v1.2.0/beads_linux.zip\n```",
        "AUTHOR_LOGIN": "newbie", "AUTHOR_ASSOCIATION": "NONE", "AUTHOR_TYPE": "User",
        "THREAD_AUTHOR_LOGIN": "other", "EVENT_NAME": "issue_comment", "_age_h": 0.1,
        "THREAD_CREATED_AT": "2026-07-07T00:00:00Z", "EVENT_CREATED_AT": "2026-07-07T00:02:00Z",
    }, "C"),
    ("L5 genuine newcomer repro.zip on OWN issue (self-issue cap)", {
        "BODY": "[repro.zip](https://github.com/user-attachments/files/8/repro.zip) minimal reproduction",
        "AUTHOR_LOGIN": "newcomer", "AUTHOR_ASSOCIATION": "NONE", "AUTHOR_TYPE": "User",
        "THREAD_AUTHOR_LOGIN": "newcomer", "EVENT_NAME": "issues", "_age_h": 0.1,
        "ACCOUNT_REPOS": "0", "ACCOUNT_FOLLOWERS": "0",
    }, {"B", "C"}),
    ("L6 bot artifact link", {
        "BODY": "[build.zip](https://github.com/user-attachments/files/9/build.zip)",
        "AUTHOR_LOGIN": "github-actions[bot]", "AUTHOR_ASSOCIATION": "NONE", "AUTHOR_TYPE": "Bot",
        "THREAD_AUTHOR_LOGIN": "x", "EVENT_NAME": "issue_comment", "_age_h": 100,
    }, "SKIP"),
]


def selftest():
    ok = True
    for name, ev, expected in SAMPLES:
        v = evaluate(ev)
        tier = v["tier"]
        passed = tier in expected if isinstance(expected, set) else tier == expected
        ok = ok and passed
        exp = "/".join(sorted(expected)) if isinstance(expected, set) else expected
        print(f"[{'PASS' if passed else 'FAIL'}] {name}: got {tier} (score {v['score']}), want {exp}")
        if not passed:
            print(f"        reason={v.get('reason')} findings={json.dumps(v.get('findings'))}")
    print("\nSELFTEST", "OK" if ok else "FAILED")
    return 0 if ok else 1


def main():
    if "--selftest" in sys.argv:
        sys.exit(selftest())
    ev = {k: os.environ.get(k, "") for k in (
        "BODY", "AUTHOR_LOGIN", "AUTHOR_ASSOCIATION", "AUTHOR_TYPE",
        "THREAD_AUTHOR_LOGIN", "THREAD_CREATED_AT", "EVENT_CREATED_AT",
        "EVENT_NAME", "ACCOUNT_CREATED_AT", "ACCOUNT_REPOS", "ACCOUNT_FOLLOWERS")}
    # BODY may arrive base64-encoded (BODY_B64) to dodge shell/env quoting issues
    if os.environ.get("BODY_B64"):
        ev["BODY"] = base64.b64decode(os.environ["BODY_B64"]).decode("utf-8", "replace")
    v = evaluate(ev)
    tier = v["tier"]
    label = {"A": "would-hard-action (report-only)", "B": "would-quarantine (report-only)",
             "C": "log-only", "SKIP": "skipped"}.get(tier, tier)
    print(f"REPORT-ONLY VERDICT: tier {tier} — {label}")
    print(f"  author={ev['AUTHOR_LOGIN']} assoc={ev['AUTHOR_ASSOCIATION']} event={ev['EVENT_NAME']}")
    print(f"  score={v.get('score')} reason={v.get('reason')}")
    for f in v.get("findings", []):
        print(f"  url={f['url']} cls={f['cls']} score={f['score']} bait={f['bait']} in_code={f['in_code']}")
    print("JSON " + json.dumps({"tier": tier, "score": v.get("score"),
                                "reason": v.get("reason"),
                                "author": ev["AUTHOR_LOGIN"],
                                "association": ev["AUTHOR_ASSOCIATION"],
                                "event": ev["EVENT_NAME"],
                                "findings": [{"url": f["url"], "cls": f["cls"], "score": f["score"]}
                                             for f in v.get("findings", [])]}))
    # report-only: always exit 0, never signal an action
    sys.exit(0)


if __name__ == "__main__":
    main()
