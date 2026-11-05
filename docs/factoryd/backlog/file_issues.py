#!/usr/bin/env python3
"""File the factoryd backlog (E*.md in this folder) as GitHub issues.

Usage:
  file_issues.py check                 parse, resolve dependencies, print the plan
  file_issues.py create --repo O/R     create labels, epics and stories (REST API via `gh api`)
                 [--queue E01,E02]     epics whose stories get the queue label; the rest are drafts

Files use the product owner's format (EPIC: / --- / STORY: / DEPENDS:), with
one extension: DEPENDS may name a story of another epic as E05.3.
Creating writes plan.md (ORDER and AFTER lines for factory/jobs/plan.md) and
issues.json (story id → issue number) next to this script.
"""

import json
import re
import subprocess
import sys
from pathlib import Path

HERE = Path(__file__).resolve().parent
BASE_BRANCH = "arch/factoryd-go"
LABELS = {"epic": "5319e7", "factory:draft": "ededed", "factory": "0e8a16", "factoryd": "1d76db"}


def die(msg):
    sys.exit(f"file_issues: {msg}")


def parse():
    epics = []
    for path in sorted(HERE.glob("E*.md")):
        eid = path.name.split("-")[0]  # E01
        blocks = re.split(r"^---\s*$", path.read_text(), flags=re.M)
        m = re.match(r"EPIC:\s*(.+)", blocks[0].strip())
        if not m:
            die(f"{path.name}: must start with EPIC:")
        epic = {"id": eid, "title": m.group(1).strip(), "body": blocks[0].strip()[m.end():].strip(), "stories": []}
        for i, block in enumerate(b.strip() for b in blocks[1:] if b.strip()):
            m = re.match(r"STORY:\s*(.+)\nDEPENDS:\s*(.*)\n", block + "\n")
            if not m:
                die(f"{path.name} block {i + 1}: needs STORY: and DEPENDS: lines")
            deps = []
            for ref in re.split(r"[,\s]+", m.group(2).strip()):
                if not ref or ref == "none":
                    continue
                deps.append(ref if ref.startswith("E") else f"{eid}.{int(ref)}")
            epic["stories"].append({"id": f"{eid}.{i + 1}", "title": m.group(1).strip(),
                                    "deps": deps, "body": block[m.end():].strip()})
        epics.append(epic)
    return epics


def topo(epics):
    stories = {s["id"]: s for e in epics for s in e["stories"]}
    for s in stories.values():
        for d in s["deps"]:
            if d not in stories:
                die(f"{s['id']} depends on unknown {d}")
    order, state = [], {}

    def visit(sid, path):
        if state.get(sid) == "done":
            return
        if state.get(sid) == "busy":
            die(f"dependency cycle: {' > '.join(path + [sid])}")
        state[sid] = "busy"
        for d in stories[sid]["deps"]:
            visit(d, path + [sid])
        state[sid] = "done"
        order.append(sid)

    for e in epics:  # keep epic order where dependencies allow
        for s in e["stories"]:
            visit(s["id"], [])
    return stories, order


def gh(*args, data=None):
    cmd = ["gh", "api", *args]
    res = subprocess.run(cmd, input=json.dumps(data) if data is not None else None,
                         capture_output=True, text=True)
    if res.returncode != 0:
        die(f"gh api {' '.join(args[:3])}: {(res.stderr or res.stdout).strip()}")
    return json.loads(res.stdout) if res.stdout.strip() else None


def create(repo, queued):
    epics = parse()
    stories, order = topo(epics)
    existing = {l["name"] for l in gh(f"repos/{repo}/labels?per_page=100")}
    for name, color in LABELS.items():
        if name not in existing:
            gh(f"repos/{repo}/labels", "--method", "POST", "--input", "-", data={"name": name, "color": color})

    def issue(title, body, labels):
        return gh(f"repos/{repo}/issues", "--method", "POST", "--input", "-",
                  data={"title": title, "body": body, "labels": labels})["number"]

    epic_n = {}
    for e in epics:
        epic_n[e["id"]] = issue(f"[{e['id']}] {e['title']}", e["body"] + "\n\n## Historias\n\n(cargando…)", ["epic", "factoryd"])
    num = {}
    for sid in order:
        s = stories[sid]
        eid = sid.split(".")[0]
        deps = "".join(f"\nDepends on #{num[d]}." for d in s["deps"])
        body = (f"{s['body']}\n\n## Contexto\nRama base: `{BASE_BRANCH}`. Leer `AGENTS.md` y `docs/factoryd/README.md`."
                f"\n\nPart of epic #{epic_n[eid]}.{deps}")
        label = "factory" if eid in queued else "factory:draft"
        num[sid] = issue(f"[{sid}] {s['title']}", body, [label, "factoryd"])
        print(f"{sid} → #{num[sid]} ({label})")
    for e in epics:
        tasks = "\n".join(f"- [ ] #{num[s['id']]} {s['title']}" for s in e["stories"])
        gh(f"repos/{repo}/issues/{epic_n[e['id']]}", "--method", "PATCH", "--input", "-",
           data={"body": e["body"] + f"\n\n## Historias\n\n{tasks}\n"})
    lines = ["ORDER: " + " ".join(str(num[sid]) for sid in order)]
    lines += [f"AFTER: {num[sid]} > " + " ".join(str(num[d]) for d in stories[sid]["deps"])
              for sid in order if stories[sid]["deps"]]
    (HERE / "plan.md").write_text("\n".join(lines) + "\n")
    (HERE / "issues.json").write_text(json.dumps({"epics": epic_n, "stories": num}, indent=2) + "\n")
    print(f"{len(epics)} epics, {len(num)} stories; plan.md and issues.json written")


def main():
    if len(sys.argv) < 2 or sys.argv[1] not in ("check", "create"):
        die(__doc__)
    if sys.argv[1] == "check":
        epics = parse()
        stories, order = topo(epics)
        for e in epics:
            print(f"{e['id']} {e['title']} ({len(e['stories'])})")
        print(f"{len(stories)} stories; start order: {' '.join(order)}")
        return
    args = sys.argv[2:]
    repo = args[args.index("--repo") + 1] if "--repo" in args else die("--repo O/R is required")
    queued = set(args[args.index("--queue") + 1].split(",")) if "--queue" in args else set()
    create(repo, queued)


if __name__ == "__main__":
    main()
