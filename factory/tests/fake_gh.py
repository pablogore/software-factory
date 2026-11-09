#!/usr/bin/env python3
"""A tiny stand-in for the `gh` CLI, with GitHub's state in $FAKE_GH_STATE.

Supports only the calls the factory makes. Tests put it first on PATH as `gh`.
"""

import json
import os
import sys
from datetime import datetime, timezone

STATE = os.environ["FAKE_GH_STATE"]
URL = "https://github.com/acme/app"


def now():
    return datetime.now(timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ")


def load():
    if os.path.exists(STATE):
        return json.load(open(STATE))
    return {"user": "pablo", "next": 1, "issues": {}, "prs": {}, "labels": []}


def save(s):
    json.dump(s, open(STATE, "w"), indent=1)


def opts(args):
    """--flag value pairs and bare --flags; positionals in order."""
    o, pos, i = {}, [], 0
    while i < len(args):
        a = args[i]
        if a.startswith("--"):
            if i + 1 < len(args) and not args[i + 1].startswith("--"):
                o.setdefault(a[2:], []).append(args[i + 1])
                i += 2
                continue
            o[a[2:]] = [True]
        else:
            pos.append(a)
        i += 1
    return o, pos


def pick(obj, fields):
    return {f: obj[f] for f in fields.split(",")}


def issue_view(s, n):
    i = s["issues"][str(n)]
    return {**i, "author": {"login": i["author"]}, "labels": [{"name": l} for l in i["labels"]],
            "url": f"{URL}/issues/{n}"}


def find_pr(s, ref):
    if ref in s["prs"]:
        return s["prs"][ref]
    for p in s["prs"].values():
        if p["head"] == ref and p["state"] == "OPEN":
            return p
    sys.exit(f"no pull requests found for branch {ref!r}")


def main(argv):
    s = load()
    group, cmd, rest = argv[0], argv[1] if len(argv) > 1 else "", argv[2:]
    o, pos = opts(rest)
    me = s["user"]
    if group == "api":
        if cmd == "user":
            print(me)
        elif cmd.endswith("/comments"):
            pr = s["prs"][cmd.split("/")[-2]]
            for c in pr["line_comments"]:
                print(json.dumps(c))
        return
    if group == "label":
        s["labels"] = sorted(set(s["labels"]) | {pos[0]})
    elif group == "issue" and cmd == "create":
        n = s["next"]; s["next"] += 1
        s["issues"][str(n)] = {"number": n, "title": o["title"][0], "body": o["body"][0], "author": me,
                               "labels": o.get("label", []), "state": "OPEN", "comments": []}
        print(f"{URL}/issues/{n}")
    elif group == "issue" and cmd == "view":
        print(json.dumps(pick(issue_view(s, pos[0]), o["json"][0])))
    elif group == "issue" and cmd == "list":
        want = o["label"][0]
        out = [pick(issue_view(s, n), o["json"][0]) for n, i in s["issues"].items()
               if want in i["labels"] and i["state"] == o.get("state", ["open"])[0].upper()]
        print(json.dumps(out))
    elif group == "issue" and cmd == "comment":
        s["issues"][pos[0]]["comments"].append({"author": {"login": me}, "body": o["body"][0], "createdAt": now()})
    elif group == "issue" and cmd == "close":
        s["issues"][pos[0]]["state"] = "CLOSED"
    elif group == "issue" and cmd == "edit":
        i = s["issues"][pos[0]]
        for l in ",".join(o.get("add-label", [])).split(","):
            if l and l not in i["labels"]:
                i["labels"].append(l)
        for l in ",".join(o.get("remove-label", [])).split(","):
            if l in i["labels"]:
                i["labels"].remove(l)
        if "body" in o:
            i["body"] = o["body"][0]
    elif group == "pr" and cmd == "create":
        n = s["next"]; s["next"] += 1
        s["prs"][str(n)] = {"number": n, "head": o["head"][0], "base": o["base"][0], "title": o["title"][0],
                            "body": o["body"][0], "state": "OPEN", "isDraft": "draft" in o,
                            "comments": [], "reviews": [], "line_comments": [], "url": f"{URL}/pull/{n}"}
        print(f"{URL}/pull/{n}")
    elif group == "pr" and cmd == "list":
        out = [pick(p, o["json"][0]) for p in s["prs"].values()
               if p["head"] == o["head"][0] and p["state"] == "OPEN"]
        print(json.dumps(out))
    elif group == "pr" and cmd == "view":
        p = find_pr(s, pos[0])
        print(json.dumps(pick({**p, "baseRefName": p["base"]}, o["json"][0])))
    elif group == "pr" and cmd == "ready":
        find_pr(s, pos[0])["isDraft"] = "undo" in o
    elif group == "pr" and cmd == "comment":
        find_pr(s, pos[0])["comments"].append({"author": {"login": me}, "authorAssociation": "OWNER",
                                               "body": o["body"][0], "createdAt": now()})
    elif group == "pr" and cmd == "merge":
        pr = find_pr(s, pos[0])
        if pr["isDraft"]:
            sys.exit("pull request is still a draft")
        pr["state"] = "MERGED"
        words = pr["body"].split()
        for prev, word in zip(words, words[1:]):  # only "Closes #n" closes an issue; "Refs #n" doesn't
            if prev == "Closes" and word.startswith("#") and word[1:].isdigit() and word[1:] in s["issues"]:
                s["issues"][word[1:]]["state"] = "CLOSED"
    elif group == "pr" and cmd == "close":
        find_pr(s, pos[0])["state"] = "CLOSED"
    else:
        sys.exit(f"fake gh: unsupported: {' '.join(argv)}")
    save(s)


if __name__ == "__main__":
    main(sys.argv[1:])
