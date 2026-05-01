#!/usr/bin/env python3
"""End-to-end test for the paras MCP server.

Builds the binary, starts it against a temp vault, runs all 10 tools through
the MCP stdio transport, and reports pass/fail for each assertion.

Usage:
    python3 scripts/test_mcp.py
    python3 scripts/test_mcp.py -v          # show requests, responses, assertions
    python3 scripts/test_mcp.py --binary /path/to/paras
"""
import argparse
import json
import os
import shutil
import socket
import subprocess
import sys
import tempfile
import threading
import time
from pathlib import Path

PASS = "\033[32m✓\033[0m"
FAIL = "\033[31m✗\033[0m"
DIM  = "\033[2m"
RESET = "\033[0m"
CYAN  = "\033[36m"
YELLOW = "\033[33m"

_id = 0
failures = 0
verbose = False

_section_name = ""
_section_pass = 0
_section_fail = 0
_section_start = 0.0


def next_id():
    global _id
    _id += 1
    return _id


def vprint(*args, **kwargs):
    if verbose:
        print(*args, **kwargs)


def _flush_section():
    global _section_name, _section_pass, _section_fail
    if not _section_name or verbose:
        return
    total = _section_pass + _section_fail
    elapsed = time.time() - _section_start
    marks = ""
    if _section_fail:
        marks = f"  {FAIL} {_section_fail}/{total} failed"
    else:
        marks = f"  {PASS} {total}/{total}"
    print(f"  {DIM}{elapsed*1000:.0f}ms{RESET}{marks}")
    _section_name = ""
    _section_pass = 0
    _section_fail = 0


def find_free_port() -> int:
    with socket.socket(socket.AF_INET, socket.SOCK_STREAM) as s:
        s.bind(("", 0))
        s.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
        return s.getsockname()[1]


def wait_for_port(host: str, port: int, timeout: float = 10.0) -> bool:
    deadline = time.time() + timeout
    while time.time() < deadline:
        try:
            with socket.create_connection((host, port), timeout=0.5):
                return True
        except (ConnectionRefusedError, OSError):
            time.sleep(0.1)
    return False


class MCPClient:
    def __init__(self, binary: str, vault: str = "", config: str = ""):
        args = [binary]
        if config:
            args += ["--config", config]
        elif vault:
            args += ["--vault", vault]
        self.proc = subprocess.Popen(
            args,
            stdin=subprocess.PIPE,
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE,
        )
        self._stderr_lines = []
        threading.Thread(target=self._drain_stderr, daemon=True).start()

    def _drain_stderr(self):
        for line in self.proc.stderr:
            self._stderr_lines.append(line.decode(errors="replace").rstrip())

    def _send(self, msg: dict):
        data = json.dumps(msg).encode() + b"\n"
        self.proc.stdin.write(data)
        self.proc.stdin.flush()

    def _recv(self) -> dict:
        while True:
            line = self.proc.stdout.readline()
            if not line:
                raise EOFError("server closed stdout")
            line = line.strip()
            if line:
                return json.loads(line)

    def initialize(self):
        self._send({
            "jsonrpc": "2.0",
            "id": next_id(),
            "method": "initialize",
            "params": {
                "protocolVersion": "2025-03-26",
                "capabilities": {},
                "clientInfo": {"name": "paras-test", "version": "1.0"},
            },
        })
        resp = self._recv()
        self._send({
            "jsonrpc": "2.0",
            "method": "notifications/initialized",
            "params": {},
        })
        return resp

    def call(self, tool: str, arguments: dict) -> dict:
        rid = next_id()
        msg = {
            "jsonrpc": "2.0",
            "id": rid,
            "method": "tools/call",
            "params": {"name": tool, "arguments": arguments},
        }
        vprint(f"\n  {CYAN}→ {tool}{RESET}  {DIM}{json.dumps(arguments)}{RESET}")
        self._send(msg)
        while True:
            resp = self._recv()
            if "method" in resp and "id" not in resp:
                continue
            if resp.get("id") == rid:
                _log_response(tool, resp)
                return resp

    def close(self):
        try:
            self.proc.stdin.close()
        except Exception:
            pass
        self.proc.wait(timeout=5)


def _log_response(tool: str, resp: dict):
    if not verbose:
        return
    is_err, text = result_content(resp)
    try:
        parsed = json.loads(text)
        pretty = json.dumps(parsed, indent=4)
    except (json.JSONDecodeError, TypeError):
        pretty = text
    color = FAIL.replace("\033[0m", "") if is_err else PASS.replace("\033[0m", "")
    label = "error" if is_err else "ok"
    prefix = f"  {color} [{label}]{RESET} "
    for i, line in enumerate(pretty.splitlines()):
        if i == 0:
            print(f"{prefix}{line}")
        else:
            print(f"       {line}")


def result_content(resp: dict) -> tuple[bool, str]:
    if "error" in resp:
        return True, str(resp["error"])
    result = resp.get("result", {})
    content = result.get("content", [])
    is_error = result.get("isError", False)
    text = content[0].get("text", "") if content else ""
    return is_error, text


def parse_json(text: str):
    try:
        return json.loads(text)
    except json.JSONDecodeError:
        return None


def check(label: str, cond: bool, detail: str = ""):
    global failures, _section_pass, _section_fail
    if cond:
        _section_pass += 1
        if verbose:
            print(f"  {PASS} {label}")
    else:
        _section_fail += 1
        failures += 1
        print(f"  {FAIL} {label}" + (f": {DIM}{detail}{RESET}" if detail else ""))


def check_ok(label: str, resp: dict) -> tuple[bool, str]:
    is_err, text = result_content(resp)
    if not verbose and not is_err:
        pass  # silent on success unless verbose
    check(label + " (no error)", not is_err, text if is_err else "")
    return is_err, text


def check_err(label: str, resp: dict, contains: str = "") -> str:
    is_err, text = result_content(resp)
    check(label + " (returns error)", is_err, text if not is_err else "")
    if contains:
        check(label + f" (contains {contains!r})", contains in text, text)
    return text


def section(name: str):
    global _section_name, _section_start
    _flush_section()
    _section_name = name
    _section_start = time.time()
    if verbose:
        print(f"\n{YELLOW}{'─' * 50}{RESET}")
        print(f"{YELLOW}  {name}{RESET}")
        print(f"{YELLOW}{'─' * 50}{RESET}")
    else:
        print(f"\n{CYAN}{name}{RESET}", end="", flush=True)


# ============================================================
# Tests
# ============================================================

def run_tests(c: MCPClient):

    section("note_create")
    resp = c.call("note_create", {
        "path": "projects/hello.md",
        "title": "Hello World",
        "body": "This is a vpc configuration guide.",
        "status": "active",
        "tags": ["aws", "#Cloud"],
        "area": "infrastructure",
        "project": "vpc-setup",
    })
    is_err, text = check_ok("create projects/hello.md", resp)
    etag_hello = None
    if not is_err:
        data = parse_json(text)
        check("title set", data and data.get("Title") == "Hello World", str(data))
        check("tags normalized", data and "aws" in data.get("Tags", []), str(data))
        check("tags normalized #Cloud→cloud", data and "cloud" in data.get("Tags", []), str(data))
        check("area set", data and data.get("Area") == "infrastructure", str(data))
        check("project set", data and data.get("Project") == "vpc-setup", str(data))
        check("ETag present", data and bool(data.get("ETag")), str(data))
        etag_hello = data.get("ETag") if data else None

    resp = c.call("note_create", {"path": "projects/hello.md"})
    check_err("duplicate create → conflict", resp, "conflict")

    resp = c.call("note_create", {"path": "notes/foo.md"})
    check_err("non-PARA root → error", resp)

    resp = c.call("note_create", {"path": "../etc/passwd"})
    check_err("path traversal → error", resp)

    # Additional path escapes
    resp = c.call("note_create", {"path": "projects/\x00null.md"})
    check_err("null byte in path → error", resp)

    section("note_get")
    resp = c.call("note_get", {"scope": "personal", "path": "projects/hello.md"})
    is_err, text = check_ok("get projects/hello.md", resp)
    if not is_err:
        data = parse_json(text)
        check("body round-trips", data and "vpc configuration" in data.get("Body", ""), str(data))
        check("ETag matches create", data and data.get("ETag") == etag_hello, str(data))
        # NoteID must be minted into derived.note_id on every MCP-created note
        fm = data.get("FrontMatter", {}) if data else {}
        note_id = fm.get("Extra", {}).get("derived", {}).get("note_id", "")
        check("NoteID minted in derived.note_id", bool(note_id), str(fm))
        check("area round-trips", fm.get("Area") == "infrastructure", str(fm))
        check("project round-trips", fm.get("Project") == "vpc-setup", str(fm))

    resp = c.call("note_get", {"scope": "personal", "path": "projects/nope.md"})
    check_err("get missing → not_found", resp, "not_found")

    section("note_update_body")
    resp = c.call("note_update_body", {
        "scope": "personal",
        "path": "projects/hello.md",
        "body": "Updated body content.",
        "if_match": etag_hello,
    })
    is_err, text = check_ok("update body with correct ETag", resp)
    etag_hello2 = None
    if not is_err:
        data = parse_json(text)
        check("ETag rotated after update", data and data.get("ETag") != etag_hello, str(data))
        etag_hello2 = data.get("ETag") if data else None

    # Verify body actually changed
    resp = c.call("note_get", {"scope": "personal", "path": "projects/hello.md"})
    is_err, text = check_ok("get after body update", resp)
    if not is_err:
        data = parse_json(text)
        check("body contains new content", data and "Updated body content" in data.get("Body", ""), str(data))
        check("body no longer has old content", data and "vpc configuration" not in data.get("Body", ""), str(data))

    resp = c.call("note_update_body", {
        "scope": "personal",
        "path": "projects/hello.md",
        "body": "This should fail.",
        "if_match": etag_hello,  # stale
    })
    conflict_text = check_err("stale ETag → conflict", resp, "conflict")
    conflict_data = parse_json(conflict_text)
    check("conflict response is JSON with error=conflict",
          conflict_data is not None and conflict_data.get("error") == "conflict",
          conflict_text)

    # Force overwrite (no if_match)
    resp = c.call("note_update_body", {
        "scope": "personal",
        "path": "projects/hello.md",
        "body": "Force overwrite body.",
    })
    is_err, text = check_ok("update body without if_match (force overwrite)", resp)
    if not is_err:
        data = parse_json(text)
        etag_hello2 = data.get("ETag") if data else etag_hello2

    section("note_patch_frontmatter")
    resp = c.call("note_patch_frontmatter", {
        "scope": "personal",
        "path": "projects/hello.md",
        "fields": {"status": "done", "title": "Hello Done"},
        "if_match": etag_hello2,
    })
    is_err, text = check_ok("patch frontmatter", resp)
    etag_hello3 = None
    if not is_err:
        data = parse_json(text)
        check("status updated", data and data.get("Status") == "done", str(data))
        check("title updated", data and data.get("Title") == "Hello Done", str(data))
        check("ETag rotated after patch", data and data.get("ETag") != etag_hello2, str(data))
        etag_hello3 = data.get("ETag") if data else None

    # Stale ETag on patch → conflict
    resp = c.call("note_patch_frontmatter", {
        "scope": "personal",
        "path": "projects/hello.md",
        "fields": {"status": "stale"},
        "if_match": etag_hello2,  # stale
    })
    check_err("stale ETag on patch → conflict", resp, "conflict")

    # Other fields preserved after patch
    resp = c.call("note_get", {"scope": "personal", "path": "projects/hello.md"})
    is_err, text = check_ok("get after patch to verify preservation", resp)
    if not is_err:
        data = parse_json(text)
        fm = data.get("FrontMatter", {}) if data else {}
        check("area preserved after patch", fm.get("Area") == "infrastructure", str(fm))
        check("project preserved after patch", fm.get("Project") == "vpc-setup", str(fm))
        check("tags preserved after patch", "aws" in (fm.get("Tags") or []), str(fm))

    section("note_move")
    resp = c.call("note_move", {
        "scope": "personal",
        "path": "projects/hello.md",
        "new_path": "areas/hello.md",
        "if_match": etag_hello3,
    })
    is_err, text = check_ok("move to areas/", resp)
    if not is_err:
        data = parse_json(text)
        check("new path in ref", data and data.get("Ref", {}).get("Path") == "areas/hello.md", str(data))

    resp = c.call("note_get", {"scope": "personal", "path": "projects/hello.md"})
    check_err("old path gone after move", resp, "not_found")

    resp = c.call("note_get", {"scope": "personal", "path": "areas/hello.md"})
    check_ok("new path accessible after move", resp)

    # Stale ETag on move → conflict
    c.call("note_create", {"path": "projects/move-stale.md", "body": "stale move test"})
    resp2 = c.call("note_get", {"scope": "personal", "path": "projects/move-stale.md"})
    _, t2 = result_content(resp2)
    etag_stale = parse_json(t2).get("ETag") if t2 else None
    # mutate to make the ETag stale
    c.call("note_update_body", {"scope": "personal", "path": "projects/move-stale.md", "body": "mutated"})
    resp = c.call("note_move", {
        "scope": "personal",
        "path": "projects/move-stale.md",
        "new_path": "areas/move-stale.md",
        "if_match": etag_stale,
    })
    check_err("move with stale ETag → conflict", resp, "conflict")

    # Move to non-PARA root → error
    resp = c.call("note_move", {
        "scope": "personal",
        "path": "projects/move-stale.md",
        "new_path": "notes/move-stale.md",
    })
    check_err("move to non-PARA root → error", resp)

    section("note_archive")
    resp = c.call("note_create", {"path": "projects/to-archive.md", "body": "archive me"})
    _, text = result_content(resp)
    etag_arch = parse_json(text).get("ETag") if not result_content(resp)[0] else None

    resp = c.call("note_archive", {
        "scope": "personal",
        "path": "projects/to-archive.md",
        "if_match": etag_arch,
    })
    is_err, text = check_ok("archive projects/to-archive.md", resp)
    if not is_err:
        data = parse_json(text)
        check("moved to archives/", data and data.get("Ref", {}).get("Path", "").startswith("archives/"), str(data))

    # Archive with stale ETag → conflict
    resp = c.call("note_create", {"path": "projects/to-archive-stale.md", "body": "archive stale"})
    _, t2 = result_content(resp)
    etag_arch_stale = parse_json(t2).get("ETag") if t2 else None
    c.call("note_update_body", {"scope": "personal", "path": "projects/to-archive-stale.md", "body": "mutated"})
    resp = c.call("note_archive", {
        "scope": "personal",
        "path": "projects/to-archive-stale.md",
        "if_match": etag_arch_stale,
    })
    check_err("archive with stale ETag → conflict", resp, "conflict")

    section("notes_list")
    c.call("note_create", {"path": "resources/guide.md", "body": "resource content", "tags": ["aws"]})
    c.call("note_create", {"path": "projects/second.md", "status": "active",
                           "area": "myarea", "project": "myproj", "tags": ["listtag"]})

    resp = c.call("notes_list", {})
    is_err, text = check_ok("list all notes", resp)
    if not is_err:
        data = parse_json(text)
        check("returns notes", data and len(data.get("Notes", [])) > 0, str(data))
        check("total > 0", data and data.get("Total", 0) > 0, str(data))

    resp = c.call("notes_list", {"status": "active"})
    is_err, text = check_ok("list by status=active", resp)
    if not is_err:
        data = parse_json(text)
        notes = data.get("Notes", []) if data else []
        check("all returned notes are active",
              all(n.get("Status") == "active" for n in notes), str(notes))

    resp = c.call("notes_list", {"categories": ["projects"]})
    is_err, text = check_ok("list projects only", resp)
    if not is_err:
        data = parse_json(text)
        notes = data.get("Notes", []) if data else []
        check("all returned notes in projects/",
              all(n.get("Ref", {}).get("Path", "").startswith("projects/") for n in notes),
              str(notes))

    # Area filter
    resp = c.call("notes_list", {"area": "myarea"})
    is_err, text = check_ok("list by area=myarea", resp)
    if not is_err:
        data = parse_json(text)
        notes = data.get("Notes", []) if data else []
        check("area filter returns matching notes", len(notes) >= 1, str(notes))
        check("all returned notes have area=myarea",
              all(n.get("Area") == "myarea" for n in notes), str(notes))

    # Project filter
    resp = c.call("notes_list", {"project": "myproj"})
    is_err, text = check_ok("list by project=myproj", resp)
    if not is_err:
        data = parse_json(text)
        notes = data.get("Notes", []) if data else []
        check("project filter returns matching notes", len(notes) >= 1, str(notes))
        check("all returned notes have project=myproj",
              all(n.get("Project") == "myproj" for n in notes), str(notes))

    # Tags filter
    resp = c.call("notes_list", {"tags": ["listtag"]})
    is_err, text = check_ok("list by tags=[listtag]", resp)
    if not is_err:
        data = parse_json(text)
        notes = data.get("Notes", []) if data else []
        check("tag filter returns matching notes", len(notes) >= 1, str(notes))
        check("all returned notes have listtag",
              all("listtag" in (n.get("Tags") or []) for n in notes), str(notes))

    # Pagination: limit + HasMore
    resp = c.call("notes_list", {"limit": 1, "sort": "updated_at"})
    is_err, text = check_ok("list with limit=1", resp)
    if not is_err:
        data = parse_json(text)
        check("exactly 1 result", data and len(data.get("Notes", [])) == 1, str(data))
        check("HasMore=true when total > 1", data and data.get("HasMore") is True, str(data))
        check("Total > 1", data and data.get("Total", 0) > 1, str(data))

    # Offset pagination: page 2
    resp_p1 = c.call("notes_list", {"limit": 1, "offset": 0, "sort": "title"})
    resp_p2 = c.call("notes_list", {"limit": 1, "offset": 1, "sort": "title"})
    _, t1 = result_content(resp_p1)
    _, t2 = result_content(resp_p2)
    d1 = parse_json(t1)
    d2 = parse_json(t2)
    if d1 and d2:
        p1_path = d1["Notes"][0]["Ref"]["Path"] if d1.get("Notes") else ""
        p2_path = d2["Notes"][0]["Ref"]["Path"] if d2.get("Notes") else ""
        check("offset pagination returns different notes", p1_path != p2_path,
              f"p1={p1_path} p2={p2_path}")

    # Descending sort
    resp_asc = c.call("notes_list", {"sort": "title", "desc": False, "limit": 100})
    resp_desc = c.call("notes_list", {"sort": "title", "desc": True, "limit": 100})
    _, ta = result_content(resp_asc)
    _, td = result_content(resp_desc)
    da = parse_json(ta)
    dd = parse_json(td)
    if da and dd and da.get("Notes") and dd.get("Notes"):
        asc_titles = [n.get("Title", "") for n in da["Notes"]]
        desc_titles = [n.get("Title", "") for n in dd["Notes"]]
        check("desc sort is reverse of asc sort",
              asc_titles == list(reversed(desc_titles)), f"asc={asc_titles} desc={desc_titles}")

    # include_archives: archived notes excluded by default but visible with the category filter
    resp = c.call("notes_list", {"categories": ["archives"]})
    is_err, text = check_ok("list archives category", resp)
    if not is_err:
        data = parse_json(text)
        notes = data.get("Notes", []) if data else []
        check("archives category returns archived notes", len(notes) >= 1, str(notes))
        check("all notes are in archives/",
              all(n.get("Ref", {}).get("Path", "").startswith("archives/") for n in notes), str(notes))

    section("notes_search")
    c.call("note_create", {"path": "resources/searchable.md",
                           "body": "unique_searchterm_xk9 about distributed systems",
                           "title": "Unique Title Zq7"})
    time.sleep(150 / 1000)  # let the BM25 writer goroutine publish the snapshot

    resp = c.call("notes_search", {"text": "unique_searchterm_xk9", "limit": 5})
    is_err, text = check_ok("search for unique body term", resp)
    if not is_err:
        results = parse_json(text)
        check("found at least one result", results is not None and len(results) > 0, str(results))
        if results:
            check("top result has Score > 0", results[0].get("Score", 0) > 0, str(results[0]))

    # Title search (title has higher boost weight in BM25)
    resp = c.call("notes_search", {"text": "Unique Title Zq7", "limit": 5})
    is_err, text = check_ok("search by title term", resp)
    if not is_err:
        results = parse_json(text)
        check("title search returns result", results is not None and len(results) > 0, str(results))

    resp = c.call("notes_search", {"text": "xyzzy_nonexistent_term_8675309"})
    is_err, text = check_ok("search with no matches", resp)
    if not is_err:
        results = parse_json(text)
        check("empty results for unknown term", results is not None and len(results) == 0, str(results))

    section("scope_resolver")
    resp = c.call("note_get", {"scope": "wrong-scope", "path": "areas/hello.md"})
    check_err("wrong scope on note_get → error", resp)

    resp = c.call("notes_list", {})
    is_err, text = check_ok("notes_list succeeds (scopes resolver active)", resp)
    if not is_err:
        data = parse_json(text)
        check("notes_list returns non-empty result", data and data.get("Total", 0) > 0, str(data))

    resp = c.call("notes_search", {"text": "unique_searchterm_xk9"})
    is_err, text = check_ok("notes_search succeeds (scopes resolver active)", resp)
    if not is_err:
        results = parse_json(text)
        check("notes_search returns results with personal scope", results is not None, str(results))

    section("vault_stats")
    resp = c.call("vault_stats", {})
    is_err, text = check_ok("vault_stats", resp)
    if not is_err:
        data = parse_json(text)
        check("TotalNotes > 0", data and data.get("TotalNotes", 0) > 0, str(data))
        check("ByCategory is a dict", data and isinstance(data.get("ByCategory"), dict), str(data))
        by_cat = (data or {}).get("ByCategory", {})
        check("projects in ByCategory", "projects" in by_cat, str(by_cat))
        check("resources in ByCategory", "resources" in by_cat, str(by_cat))
        check("archives in ByCategory", "archives" in by_cat, str(by_cat))
        total_from_cats = sum(by_cat.values())
        check("ByCategory sums to TotalNotes",
              total_from_cats == data.get("TotalNotes", -1),
              f"sum={total_from_cats} total={data.get('TotalNotes')}")

    section("note_delete")
    resp = c.call("note_create", {"path": "projects/soft-del.md", "body": "bye"})

    resp = c.call("note_delete", {"scope": "personal", "path": "projects/soft-del.md", "soft": True})
    check_ok("soft delete", resp)

    resp = c.call("note_get", {"scope": "personal", "path": "projects/soft-del.md"})
    check_err("soft-deleted note not accessible", resp, "not_found")

    # Delete a non-existent note → not_found
    resp = c.call("note_delete", {"scope": "personal", "path": "projects/nonexistent-del.md", "soft": True})
    check_err("delete non-existent → not_found", resp, "not_found")

    c.call("note_create", {"path": "projects/hard-del.md", "body": "gone"})
    resp = c.call("note_delete", {"scope": "personal", "path": "projects/hard-del.md", "soft": False})
    check_ok("hard delete", resp)

    resp = c.call("note_get", {"scope": "personal", "path": "projects/hard-del.md"})
    check_err("hard-deleted note not accessible", resp, "not_found")

    # ETag precondition: stale if_match → conflict
    resp = c.call("note_create", {"path": "projects/del-occ.md", "body": "occ delete"})
    _, t_docc = result_content(resp)
    etag_docc = parse_json(t_docc).get("ETag") if t_docc else None
    c.call("note_update_body", {"scope": "personal", "path": "projects/del-occ.md", "body": "mutated"})
    resp = c.call("note_delete", {"scope": "personal", "path": "projects/del-occ.md", "if_match": etag_docc})
    check_err("delete with stale ETag → conflict", resp, "conflict")

    section("note_promote")
    # Single-vault mode has no cross-scope capability; promote always returns scope_forbidden.
    resp = c.call("note_promote", {"scope": "personal", "path": "projects/any.md", "to_scope": "team"})
    check_err("note_promote in single-vault → scope_forbidden", resp, "scope_forbidden")

    section("audit_search")
    # audit_search is only registered when expose_admin_tools=true; the default binary does not
    # enable it, so calling it should return a tool-not-found error from the MCP layer.
    resp = c.call("audit_search", {"scope": "personal"})
    check_err("audit_search not registered in default binary mode → error", resp)


def run_phase2_tests(c: MCPClient, vault_dir: str):

    section("vault_health")
    resp = c.call("vault_health", {})
    is_err, text = check_ok("vault_health succeeds", resp)
    if not is_err:
        data = parse_json(text)
        check("UnrecognizedFiles key present", data is not None and "UnrecognizedFiles" in data, str(data))
        check("WatcherStatus key present", data is not None and "WatcherStatus" in data, str(data))
        check("CaseCollisions key present", data is not None and "CaseCollisions" in data, str(data))
        check("SyncConflicts key present", data is not None and "SyncConflicts" in data, str(data))
        check("CaseCollisions is list or null",
              data is None or isinstance(data.get("CaseCollisions"), (list, type(None))), str(data))

    # Place a non-markdown file in the vault and verify UnrecognizedFiles increases
    unrecog_path = os.path.join(vault_dir, "projects", "unrecognized.txt")
    os.makedirs(os.path.dirname(unrecog_path), exist_ok=True)
    with open(unrecog_path, "w") as f:
        f.write("not a markdown note")
    resp = c.call("vault_health", {})
    is_err, text = check_ok("vault_health after adding unrecognized file", resp)
    if not is_err:
        data = parse_json(text)
        check("UnrecognizedFiles > 0 after adding .txt file",
              data is not None and data.get("UnrecognizedFiles", 0) > 0, str(data))

    section("vault_rescan")
    # Write a note directly to disk without going through the MCP server
    ext_dir = os.path.join(vault_dir, "resources")
    os.makedirs(ext_dir, exist_ok=True)
    ext_note = os.path.join(ext_dir, "external.md")
    with open(ext_note, "w") as f:
        f.write("---\ntitle: External Note\n---\nWritten directly to disk.\n")

    resp = c.call("vault_rescan", {})
    is_err, text = check_ok("vault_rescan succeeds", resp)
    if not is_err:
        check("returns rescan complete", "rescan" in text.lower(), text)

    # After rescan, the externally-written note should be accessible
    resp = c.call("note_get", {"scope": "personal", "path": "resources/external.md"})
    is_err, text = check_ok("externally-written note accessible after rescan", resp)
    if not is_err:
        data = parse_json(text)
        check("external note title round-trips", data and data.get("FrontMatter", {}).get("Title") == "External Note", str(data))
        # Rescan mints a NoteID for editor-created notes
        note_id = data.get("FrontMatter", {}).get("Extra", {}).get("derived", {}).get("note_id", "") if data else ""
        check("NoteID minted for editor-created note after rescan", bool(note_id), str(data))

    section("notes_stale")
    # Write a note with an old updated_at date directly to disk, then rescan
    stale_dir = os.path.join(vault_dir, "areas")
    os.makedirs(stale_dir, exist_ok=True)
    stale_note = os.path.join(stale_dir, "ancient.md")
    with open(stale_note, "w") as f:
        f.write("---\ntitle: Ancient Note\nupdated_at: 2020-01-01T00:00:00Z\ncreated_at: 2020-01-01T00:00:00Z\n---\nVery old note.\n")
    c.call("vault_rescan", {})

    # days=1: ancient note was last updated in 2020, so it IS stale
    resp = c.call("notes_stale", {"days": 1})
    is_err, text = check_ok("notes_stale with days=1 after adding old note", resp)
    if not is_err:
        data = parse_json(text)
        check("returns Notes key", data is not None and "Notes" in data, str(data))
        stale_paths = [n.get("Ref", {}).get("Path", "") for n in (data or {}).get("Notes", [])]
        check("ancient note appears in stale results",
              any("ancient" in p for p in stale_paths), str(stale_paths))

    # Recently-created notes should not appear in stale results for days=1
    resp2 = c.call("note_create", {"path": "projects/fresh.md", "body": "Just created"})
    resp = c.call("notes_stale", {"days": 1})
    is_err, text = check_ok("fresh note absent from stale results", resp)
    if not is_err:
        data = parse_json(text)
        stale_paths = [n.get("Ref", {}).get("Path", "") for n in (data or {}).get("Notes", [])]
        check("fresh note NOT in stale results",
              not any("fresh" in p for p in stale_paths), str(stale_paths))

    # notes_stale with days=0 should error
    resp = c.call("notes_stale", {"days": 0})
    check_err("days=0 → error", resp)

    # notes_stale categories filter
    resp = c.call("notes_stale", {"days": 1, "categories": ["projects"]})
    is_err, text = check_ok("notes_stale categories filter", resp)
    if not is_err:
        data = parse_json(text)
        notes = (data or {}).get("Notes", [])
        check("category filter: only projects returned",
              all(n.get("Ref", {}).get("Path", "").startswith("projects/") for n in notes), str(notes))

    section("notes_backlinks")
    # Create notes: target, regular wikilink, asset embed, full-path wikilink, unrelated
    c.call("note_create", {"path": "projects/target.md", "title": "Target Note", "body": "I am the target."})
    c.call("note_create", {"path": "projects/linker-a.md", "body": "See [[target]] for details."})
    c.call("note_create", {"path": "resources/linker-b.md", "body": "Reference ![[target]] as an asset."})
    c.call("note_create", {"path": "areas/linker-c.md", "body": "Full path: [[projects/target]]."})
    c.call("note_create", {"path": "areas/unrelated.md", "body": "No links here."})

    # Default (include_assets=false): linker-a and linker-c (full-path), not linker-b
    resp = c.call("notes_backlinks", {"scope": "personal", "path": "projects/target.md"})
    is_err, text = check_ok("backlinks (no assets)", resp)
    if not is_err:
        data = parse_json(text)
        check("returns list", isinstance(data, list), str(data))
        paths = [e.get("Summary", {}).get("Ref", {}).get("Path", "") for e in (data or [])]
        check("linker-a included (bare [[target]])", any("linker-a" in p for p in paths), str(paths))
        check("linker-c included (full-path [[projects/target]])", any("linker-c" in p for p in paths), str(paths))
        check("linker-b excluded (asset ![[target]])", not any("linker-b" in p for p in paths), str(paths))
        check("unrelated excluded", not any("unrelated" in p for p in paths), str(paths))

    # With include_assets=true: all three linkers appear; linker-b has IsAsset=true
    resp = c.call("notes_backlinks", {"scope": "personal", "path": "projects/target.md",
                                      "include_assets": True})
    is_err, text = check_ok("backlinks (include_assets=true)", resp)
    if not is_err:
        data = parse_json(text)
        check("all three linkers present", data is not None and len(data) >= 3, str(data))
        asset_entries = [e for e in (data or []) if e.get("IsAsset")]
        non_asset_entries = [e for e in (data or []) if not e.get("IsAsset")]
        check("linker-b has IsAsset=true", len(asset_entries) >= 1, str(data))
        check("linker-a/linker-c have IsAsset=false", len(non_asset_entries) >= 2, str(data))

    # Empty backlinks
    resp = c.call("notes_backlinks", {"scope": "personal", "path": "areas/unrelated.md"})
    is_err, text = check_ok("backlinks for note with no inbound links", resp)
    if not is_err:
        data = parse_json(text)
        check("empty array", data == [], str(data))

    # Backlinks after move: move linker-a; backlinks for target should reflect new path
    resp_get = c.call("note_get", {"scope": "personal", "path": "projects/linker-a.md"})
    _, tg = result_content(resp_get)
    etag_la = parse_json(tg).get("ETag") if tg else None
    c.call("note_move", {"scope": "personal", "path": "projects/linker-a.md",
                         "new_path": "areas/linker-a-moved.md", "if_match": etag_la})
    resp = c.call("notes_backlinks", {"scope": "personal", "path": "projects/target.md"})
    is_err, text = check_ok("backlinks after linker moved", resp)
    if not is_err:
        data = parse_json(text)
        paths = [e.get("Summary", {}).get("Ref", {}).get("Path", "") for e in (data or [])]
        check("moved linker appears at new path", any("linker-a-moved" in p for p in paths), str(paths))
        check("old path no longer in backlinks", not any("projects/linker-a.md" == p for p in paths), str(paths))

    section("notes_related")
    c.call("note_create", {
        "path": "projects/rel-a.md",
        "body": "Project A",
        "tags": ["go", "backend"],
        "area": "eng",
        "project": "platform",
    })
    c.call("note_create", {
        "path": "projects/rel-b.md",
        "body": "Project B shares tags",
        "tags": ["go", "backend"],
        "area": "eng",
        "project": "platform",
    })
    c.call("note_create", {
        "path": "areas/rel-d.md",
        "body": "Same area, no tags",
        "area": "eng",
    })
    c.call("note_create", {
        "path": "resources/rel-c.md",
        "body": "Unrelated resource",
        "tags": ["cooking"],
    })

    # rel-a is most related to rel-b (shared tags + area + project = highest score)
    resp = c.call("notes_related", {"scope": "personal", "path": "projects/rel-a.md"})
    is_err, text = check_ok("notes_related for rel-a", resp)
    if not is_err:
        data = parse_json(text)
        check("returns list", isinstance(data, list), str(data))
        if data:
            check("top result has Score > 0", data[0].get("Score", 0) > 0, str(data[0]))
            top_path = data[0].get("Summary", {}).get("Ref", {}).get("Path", "")
            check("top result is rel-b (shared tags+area+project)", "rel-b" in top_path, str(data))

    # Area-only overlap (rel-d shares area=eng with rel-a)
    resp = c.call("notes_related", {"scope": "personal", "path": "areas/rel-d.md"})
    is_err, text = check_ok("notes_related by area overlap", resp)
    if not is_err:
        data = parse_json(text)
        paths = [r.get("Summary", {}).get("Ref", {}).get("Path", "") for r in (data or [])]
        check("area-related notes appear", any("rel-a" in p or "rel-b" in p for p in paths), str(paths))

    # Limit parameter
    resp = c.call("notes_related", {"scope": "personal", "path": "projects/rel-a.md", "limit": 1})
    is_err, text = check_ok("notes_related with limit=1", resp)
    if not is_err:
        data = parse_json(text)
        check("limit=1 returns at most 1 result", data is not None and len(data) <= 1, str(data))

    # Unrelated note
    resp = c.call("notes_related", {"scope": "personal", "path": "resources/rel-c.md"})
    is_err, text = check_ok("notes_related with no overlap returns list", resp)
    if not is_err:
        data = parse_json(text)
        check("returns list", isinstance(data, list), str(data))

    section("notes_create_batch")
    # Mixed success/failure with path traversal
    resp = c.call("notes_create_batch", {
        "notes": [
            {"path": "projects/batch-a.md", "body": "Batch note A", "status": "active",
             "area": "batcharea", "project": "batchproj"},
            {"path": "projects/batch-b.md", "body": "Batch note B",
             "area": "batcharea", "project": "batchproj"},
            {"path": "../escape/bad.md", "body": "should fail"},
        ]
    })
    is_err, text = check_ok("notes_create_batch mixed success/failure", resp)
    if not is_err:
        data = parse_json(text)
        check("SuccessCount == 2", data and data.get("SuccessCount") == 2, str(data))
        check("FailureCount == 1", data and data.get("FailureCount") == 1, str(data))
        results = data.get("Results", []) if data else []
        check("3 results returned", len(results) == 3, str(results))
        check("result[0] index==0", results[0].get("Index") == 0 if results else False, str(results))
        check("result[2] index==2", results[2].get("Index") == 2 if len(results) > 2 else False, str(results))
        fail_results = [r for r in results if not r.get("OK")]
        check("failed result has Error", len(fail_results) == 1 and bool(fail_results[0].get("Error")), str(fail_results))

    # Empty batch succeeds with zero counts
    resp = c.call("notes_create_batch", {"notes": []})
    is_err, text = check_ok("notes_create_batch empty array succeeds", resp)
    if not is_err:
        data = parse_json(text)
        check("SuccessCount == 0", data and data.get("SuccessCount") == 0, str(data))
        check("FailureCount == 0", data and data.get("FailureCount") == 0, str(data))

    section("notes_update_batch")
    # Get ETags for ETag-protection test
    r_a = c.call("note_get", {"scope": "personal", "path": "projects/batch-a.md"})
    r_b = c.call("note_get", {"scope": "personal", "path": "projects/batch-b.md"})
    _, ta = result_content(r_a)
    _, tb = result_content(r_b)
    etag_ba = parse_json(ta).get("ETag") if ta else None
    etag_bb = parse_json(tb).get("ETag") if tb else None

    resp = c.call("notes_update_batch", {
        "notes": [
            {"path": "projects/batch-a.md", "body": "Updated body A", "if_match": etag_ba},
            {"path": "projects/batch-b.md", "body": "Updated body B"},
            {"path": "projects/nonexistent.md", "body": "should fail"},
        ]
    })
    is_err, text = check_ok("notes_update_batch mixed success/failure", resp)
    if not is_err:
        data = parse_json(text)
        check("SuccessCount == 2", data and data.get("SuccessCount") == 2, str(data))
        check("FailureCount == 1", data and data.get("FailureCount") == 1, str(data))

    # ETag conflict in batch: use stale etag for batch-b (already updated above without etag_bb)
    resp = c.call("notes_update_batch", {
        "notes": [
            {"path": "projects/batch-a.md", "body": "Fresh A"},
            {"path": "projects/batch-b.md", "body": "Should fail", "if_match": etag_bb},
        ]
    })
    is_err, text = check_ok("notes_update_batch with one stale ETag", resp)
    if not is_err:
        data = parse_json(text)
        check("SuccessCount == 1 (fresh-A ok)", data and data.get("SuccessCount") == 1, str(data))
        check("FailureCount == 1 (stale-B fails)", data and data.get("FailureCount") == 1, str(data))
        results = data.get("Results", []) if data else []
        b_result = next((r for r in results if "batch-b" in r.get("Path", "")), None)
        check("batch-b failure contains 'conflict'",
              b_result is not None and "conflict" in b_result.get("Error", "").lower(),
              str(b_result))

    section("notes_patch_frontmatter_batch")
    resp = c.call("notes_patch_frontmatter_batch", {
        "notes": [
            {"path": "projects/batch-a.md", "fields": {"status": "done"}},
            {"path": "projects/batch-b.md", "fields": {"status": "done"}},
            {"path": "projects/nonexistent.md", "fields": {"status": "done"}},
        ]
    })
    is_err, text = check_ok("notes_patch_frontmatter_batch mixed success/failure", resp)
    if not is_err:
        data = parse_json(text)
        check("SuccessCount == 2", data and data.get("SuccessCount") == 2, str(data))
        check("FailureCount == 1", data and data.get("FailureCount") == 1, str(data))

    # Verify patch actually applied
    resp = c.call("note_get", {"scope": "personal", "path": "projects/batch-a.md"})
    is_err, text = check_ok("get batch-a after patch", resp)
    if not is_err:
        data = parse_json(text)
        check("status=done applied to batch-a",
              data and data.get("FrontMatter", {}).get("Status") == "done", str(data))
        # area/project from create still preserved
        check("area preserved through patch",
              data and data.get("FrontMatter", {}).get("Area") == "batcharea", str(data))

    section("category_templates")
    # projects → status=active applied by default template
    resp = c.call("note_create", {"path": "projects/templated.md", "title": "Templated Project"})
    is_err, text = check_ok("create note in projects/ (template applies)", resp)
    if not is_err:
        data = parse_json(text)
        check("status=active from template", data and data.get("Status") == "active", str(data))

    # archives → status=archived applied by default template
    resp = c.call("note_create", {"path": "archives/templated-archive.md", "title": "Archived"})
    is_err, text = check_ok("create note in archives/ (template applies)", resp)
    if not is_err:
        data = parse_json(text)
        check("status=archived from template", data and data.get("Status") == "archived", str(data))

    # areas/resources → no default status (empty)
    resp = c.call("note_create", {"path": "areas/no-template.md", "title": "Area Note"})
    is_err, text = check_ok("create note in areas/ (no status template)", resp)
    if not is_err:
        data = parse_json(text)
        check("areas note has no default status", data and data.get("Status") == "", str(data))

    resp = c.call("note_create", {"path": "resources/no-template.md", "title": "Resource Note"})
    is_err, text = check_ok("create note in resources/ (no status template)", resp)
    if not is_err:
        data = parse_json(text)
        check("resources note has no default status", data and data.get("Status") == "", str(data))

    # Explicit status overrides template
    resp = c.call("note_create", {"path": "projects/explicit-status.md", "status": "paused"})
    is_err, text = check_ok("explicit status overrides template", resp)
    if not is_err:
        data = parse_json(text)
        check("status=paused (explicit wins over template)", data and data.get("Status") == "paused", str(data))


def run_federation_tests(binary: str):
    """
    Starts a remote paras HTTP server (team scope), builds a gateway config,
    starts the gateway in stdio mode, and verifies federated reads.
    """
    remote_vault = tempfile.mkdtemp(prefix="paras-remote-")
    config_path = None
    remote_proc = None
    gw = None

    try:
        # --- Populate remote vault directly on disk ---
        for cat in ("projects", "resources"):
            os.makedirs(os.path.join(remote_vault, cat), exist_ok=True)

        # 5 team notes for pagination tests
        for i in range(5):
            with open(os.path.join(remote_vault, "projects", f"team-proj-{i}.md"), "w") as f:
                f.write(
                    f"---\ntitle: Team Project {i}\ntags:\n  - team\nstatus: active\n"
                    f"---\nTeam project {i} distributed_systems_xq9.\n"
                )
        with open(os.path.join(remote_vault, "resources", "team-guide.md"), "w") as f:
            f.write("---\ntitle: Team Engineering Guide\ntags:\n  - engineering\n---\nEngineering documentation.\n")

        # Populate local vault
        local_vault = tempfile.mkdtemp(prefix="paras-local-fed-")
        try:
            os.makedirs(os.path.join(local_vault, "projects"), exist_ok=True)
            for i in range(3):
                with open(os.path.join(local_vault, "projects", f"personal-proj-{i}.md"), "w") as f:
                    f.write(
                        f"---\ntitle: Personal Project {i}\ntags:\n  - personal\n"
                        f"---\nPersonal project {i}.\n"
                    )

            # --- Start remote HTTP server ---
            port = find_free_port()
            remote_proc = subprocess.Popen(
                [binary, "--vault", remote_vault, "--scope", "team", "--addr", f":{port}"],
                stdout=subprocess.DEVNULL,
                stderr=subprocess.DEVNULL,
            )

            section("federation/setup")
            if not wait_for_port("localhost", port):
                check("remote server started", False, f"timed out on port {port}")
                return
            check("remote server started", True)

            # --- Write gateway config ---
            fd, config_path = tempfile.mkstemp(suffix=".yaml")
            with os.fdopen(fd, "w") as f:
                f.write(
                    f"local:\n  vault: {local_vault}\n  scope: personal\n"
                    f"remotes:\n  - scope: team\n    url: http://localhost:{port}/mcp\n"
                )

            # --- Start gateway (connects to remote synchronously during startup) ---
            gw = MCPClient(binary, config=config_path)
            gw.initialize()

            # --- vault_list_scopes ---
            section("federation/vault_list_scopes")
            resp = gw.call("vault_list_scopes", {})
            is_err, text = check_ok("lists both scopes", resp)
            if not is_err:
                scopes_data = parse_json(text) or []
                scope_ids = [s.get("scope") for s in scopes_data]
                check("personal scope present", "personal" in scope_ids, str(scope_ids))
                check("team scope present", "team" in scope_ids, str(scope_ids))

            # --- federated notes_list ---
            section("federation/notes_list")
            resp = gw.call("notes_list", {"limit": 100})
            is_err, text = check_ok("returns notes from both scopes", resp)
            if not is_err:
                data = parse_json(text) or {}
                notes = data.get("Notes", [])
                scopes_seen = {n.get("Ref", {}).get("Scope") for n in notes}
                check("personal notes included", "personal" in scopes_seen, str(scopes_seen))
                check("team notes included", "team" in scopes_seen, str(scopes_seen))
                check("ScopesSucceeded has both",
                      set(data.get("ScopesSucceeded") or []) == {"personal", "team"},
                      str(data.get("ScopesSucceeded")))

            # --- cursor pagination across scopes ---
            resp1 = gw.call("notes_list", {"limit": 2, "sort": "title"})
            is_err1, text1 = check_ok("cursor pagination page 1", resp1)
            if not is_err1:
                d1 = parse_json(text1) or {}
                check("HasMore=true", d1.get("HasMore") is True, str(d1))
                cursor = d1.get("NextCursor", "")
                check("NextCursor is set", bool(cursor), str(d1))
                if cursor:
                    resp2 = gw.call("notes_list", {"limit": 2, "sort": "title", "cursor": cursor})
                    is_err2, text2 = check_ok("cursor pagination page 2", resp2)
                    if not is_err2:
                        d2 = parse_json(text2) or {}
                        p1 = {n.get("Ref", {}).get("Path") for n in d1.get("Notes", [])}
                        p2 = {n.get("Ref", {}).get("Path") for n in d2.get("Notes", [])}
                        check("page 2 has no overlap with page 1", not (p1 & p2),
                              f"overlap: {p1 & p2}")

            # offset cap
            resp = gw.call("notes_list", {"offset": 501})
            check_err("offset > 500 → error", resp, "500")

            # expired / malformed cursor
            resp = gw.call("notes_list", {"cursor": "notvalidbase64!!"})
            check_err("malformed cursor → invalid_argument", resp, "invalid_argument")

            # --- federated notes_search ---
            section("federation/notes_search")
            time.sleep(0.3)  # let both vaults build their BM25 index
            resp = gw.call("notes_search", {"text": "distributed_systems_xq9", "limit": 10})
            is_err, text = check_ok("federated search finds remote term", resp)
            if not is_err:
                results = parse_json(text) or []
                team_hits = [r for r in results
                             if r.get("Summary", {}).get("Ref", {}).get("Scope") == "team"]
                check("team notes appear in search results", len(team_hits) > 0, str(results))

            # --- federation writes: mutations forwarded to remote scope ---
            section("federation/federation_writes")
            resp = gw.call("note_get", {"scope": "team", "path": "projects/team-proj-0.md"})
            is_err, text = check_ok("get team note for write test", resp)
            team_write_etag = None
            if not is_err:
                team_write_etag = parse_json(text).get("ETag") if text else None

            resp = gw.call("note_update_body", {
                "scope": "team", "path": "projects/team-proj-0.md",
                "body": "Updated by gateway.", "if_match": team_write_etag,
            })
            is_err, text = check_ok("update body on remote scope via gateway", resp)
            if not is_err:
                data = parse_json(text)
                check("ETag rotated after remote write", data and data.get("ETag") != team_write_etag, str(data))

            resp = gw.call("note_get", {"scope": "team", "path": "projects/team-proj-0.md"})
            is_err, text = check_ok("get remote note after gateway write", resp)
            if not is_err:
                data = parse_json(text)
                check("remote body updated via gateway",
                      data and "Updated by gateway" in (data.get("Body") or ""), str(data))

            resp = gw.call("note_update_body", {
                "scope": "team", "path": "projects/team-proj-0.md",
                "body": "stale overwrite attempt", "if_match": team_write_etag,  # stale
            })
            check_err("remote write with stale ETag → conflict", resp, "conflict")

            # --- note_promote: cross-scope copy with ETag, on_conflict, idempotency_key ---
            section("federation/note_promote")
            resp = gw.call("note_create", {"path": "projects/promo-test.md", "body": "promote me", "title": "Promo Test"})
            is_err, text = check_ok("create personal note for promote", resp)
            promo_etag1 = parse_json(text).get("ETag") if (not is_err and text) else None

            # Stale ETag → conflict
            if promo_etag1:
                gw.call("note_update_body", {"scope": "personal", "path": "projects/promo-test.md", "body": "v2"})
                resp = gw.call("note_promote", {
                    "scope": "personal", "path": "projects/promo-test.md",
                    "to_scope": "team", "if_match": promo_etag1,
                })
                check_err("promote with stale source ETag → conflict", resp, "conflict")

            # Fresh ETag, keep_source=false → source archived
            resp = gw.call("note_get", {"scope": "personal", "path": "projects/promo-test.md"})
            _, tg = result_content(resp)
            promo_etag2 = parse_json(tg).get("ETag") if tg else None
            resp = gw.call("note_promote", {
                "scope": "personal", "path": "projects/promo-test.md",
                "to_scope": "team", "if_match": promo_etag2, "keep_source": False,
            })
            is_err, text = check_ok("promote personal→team with fresh ETag", resp)
            if not is_err:
                data = parse_json(text)
                check("result scope is team",
                      data and data.get("Ref", {}).get("Scope") == "team", str(data))
                check("result path matches",
                      data and data.get("Ref", {}).get("Path") == "projects/promo-test.md", str(data))
                check("ETag present on promote result", data and bool(data.get("ETag")), str(data))

            resp = gw.call("note_get", {"scope": "personal", "path": "projects/promo-test.md"})
            check_err("source archived after promote (keep_source=false)", resp, "not_found")

            resp = gw.call("note_get", {"scope": "team", "path": "projects/promo-test.md"})
            is_err, text = check_ok("promoted note accessible in team scope", resp)
            if not is_err:
                data = parse_json(text)
                check("promoted body in team", data and "v2" in (data.get("Body") or ""), str(data))
                fm = (data.get("FrontMatter") or {}) if data else {}
                note_id_team = fm.get("Extra", {}).get("derived", {}).get("note_id", "")
                check("promoted note has a NoteID", bool(note_id_team), str(fm))

            # on_conflict: error / overwrite
            resp = gw.call("note_create", {"path": "projects/clash-src.md", "body": "clash"})
            is_err_c, _ = check_ok("create clash-src for on_conflict test", resp)
            if not is_err_c:
                gw.call("note_promote", {
                    "scope": "personal", "path": "projects/clash-src.md",
                    "to_scope": "team", "keep_source": True,
                })
                resp = gw.call("note_promote", {
                    "scope": "personal", "path": "projects/clash-src.md",
                    "to_scope": "team", "on_conflict": "error", "keep_source": True,
                })
                check_err("promote on_conflict=error when dest exists → conflict", resp, "conflict")
                resp = gw.call("note_promote", {
                    "scope": "personal", "path": "projects/clash-src.md",
                    "to_scope": "team", "on_conflict": "overwrite", "keep_source": True,
                })
                check_ok("promote on_conflict=overwrite when dest exists → ok", resp)

            # idempotency_key: second call returns cached result
            resp = gw.call("note_create", {"path": "projects/idem-promo.md", "body": "idempotent"})
            is_err_i, _ = check_ok("create idem-promo for idempotency test", resp)
            if not is_err_i:
                resp1 = gw.call("note_promote", {
                    "scope": "personal", "path": "projects/idem-promo.md",
                    "to_scope": "team", "keep_source": True,
                    "idempotency_key": "e2e-idem-001",
                })
                is_err1, text1 = check_ok("first promote with idempotency_key", resp1)
                resp2 = gw.call("note_promote", {
                    "scope": "personal", "path": "projects/idem-promo.md",
                    "to_scope": "team", "keep_source": True,
                    "idempotency_key": "e2e-idem-001",
                })
                is_err2, text2 = check_ok("second promote same idempotency_key (cached)", resp2)
                if not is_err1 and not is_err2:
                    d1, d2 = parse_json(text1), parse_json(text2)
                    check("idempotency: same ETag on retry",
                          d1 and d2 and d1.get("ETag") == d2.get("ETag"),
                          f"first={d1.get('ETag') if d1 else None} second={d2.get('ETag') if d2 else None}")

            # --- partial failure: stop remote, re-query ---
            section("federation/partial_failure")
            remote_proc.terminate()
            try:
                remote_proc.wait(timeout=3)
            except subprocess.TimeoutExpired:
                remote_proc.kill()
                remote_proc.wait(timeout=3)
            remote_proc = None
            time.sleep(0.2)  # let connections drain

            resp = gw.call("notes_list", {"limit": 50})
            is_err, text = check_ok("partial failure returns personal notes", resp)
            if not is_err:
                data = parse_json(text) or {}
                notes = data.get("Notes", [])
                scopes_returned = {n.get("Ref", {}).get("Scope") for n in notes}
                check("personal notes still returned", "personal" in scopes_returned, str(scopes_returned))
                check("team notes absent", "team" not in scopes_returned, str(scopes_returned))
                pf = data.get("PartialFailure")
                check("PartialFailure is non-null", pf is not None, str(data))
                if pf:
                    check("team in FailedScopes",
                          "team" in (pf.get("FailedScopes") or []), str(pf))
                    check("WarningText is non-empty", bool(pf.get("WarningText")), str(pf))

        finally:
            shutil.rmtree(local_vault, ignore_errors=True)

    finally:
        if gw:
            gw.close()
        if remote_proc:
            remote_proc.terminate()
            try:
                remote_proc.wait(timeout=3)
            except subprocess.TimeoutExpired:
                remote_proc.kill()
                remote_proc.wait(timeout=3)
        if config_path and os.path.exists(config_path):
            os.unlink(config_path)
        shutil.rmtree(remote_vault, ignore_errors=True)


def run_promotion_approval_tests(binary: str):
    """
    Verifies the require_promotion_approval flag (ADR-0006).

    Starts a remote server (team scope), then starts a gateway with
    --require-promotion-approval. Checks that note_promote returns a
    non-error pending_approval status rather than executing immediately.

    Note: there is currently no approval/reject endpoint — that workflow
    is deferred to post-Phase-5. The flag ships infra only.
    """
    remote_vault = tempfile.mkdtemp(prefix="paras-promo-approval-remote-")
    local_vault = tempfile.mkdtemp(prefix="paras-promo-approval-local-")
    config_path = None
    remote_proc = None
    gw = None

    try:
        os.makedirs(os.path.join(remote_vault, "projects"), exist_ok=True)
        os.makedirs(os.path.join(local_vault, "projects"), exist_ok=True)

        port = find_free_port()
        remote_proc = subprocess.Popen(
            [binary, "--vault", remote_vault, "--scope", "team", "--addr", f":{port}"],
            stdout=subprocess.DEVNULL,
            stderr=subprocess.DEVNULL,
        )

        section("promotion_approval/setup")
        if not wait_for_port("localhost", port):
            check("remote server started", False, f"timed out on port {port}")
            return
        check("remote server started", True)

        fd, config_path = tempfile.mkstemp(suffix=".yaml")
        with os.fdopen(fd, "w") as f:
            f.write(
                f"local:\n  vault: {local_vault}\n  scope: personal\n"
                f"remotes:\n  - scope: team\n    url: http://localhost:{port}/mcp\n"
            )

        # Start gateway with --require-promotion-approval.
        args = [binary, "--config", config_path, "--require-promotion-approval"]
        gw_proc = subprocess.Popen(
            args,
            stdin=subprocess.PIPE,
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE,
        )
        gw = MCPClient.__new__(MCPClient)
        gw.proc = gw_proc
        gw._stderr_lines = []
        threading.Thread(target=gw._drain_stderr, daemon=True).start()
        gw.initialize()

        # Seed a personal note to promote.
        resp = gw.call("note_create", {"path": "projects/approval-test.md",
                                       "body": "pending promotion", "title": "Approval Test"})
        is_err, _ = check_ok("create personal note for approval test", resp)
        if is_err:
            return

        section("promotion_approval/note_promote_returns_pending")
        resp = gw.call("note_promote", {
            "scope": "personal",
            "path": "projects/approval-test.md",
            "to_scope": "team",
        })
        is_err, text = result_content(resp)
        check("promote returns non-error result (not rejected)", not is_err, text)
        data = parse_json(text) if not is_err else None
        check("status=pending_approval in response",
              data is not None and data.get("status") == "pending_approval",
              text)

        section("promotion_approval/note_not_promoted")
        # The note must NOT have been written to the remote scope.
        resp = gw.call("note_get", {"scope": "team", "path": "projects/approval-test.md"})
        is_err2, _ = result_content(resp)
        check("note absent from team scope (pending, not executed)", is_err2)

    finally:
        if gw and hasattr(gw, "proc"):
            try:
                gw.proc.stdin.close()
            except Exception:
                pass
            gw.proc.wait(timeout=5)
        if remote_proc:
            remote_proc.terminate()
            try:
                remote_proc.wait(timeout=3)
            except subprocess.TimeoutExpired:
                remote_proc.kill()
                remote_proc.wait(timeout=3)
        if config_path and os.path.exists(config_path):
            os.unlink(config_path)
        shutil.rmtree(remote_vault, ignore_errors=True)
        shutil.rmtree(local_vault, ignore_errors=True)


def main():
    global verbose

    parser = argparse.ArgumentParser(description=__doc__,
                                     formatter_class=argparse.RawDescriptionHelpFormatter)
    parser.add_argument("--binary", default="", help="path to pre-built paras binary")
    parser.add_argument("-v", "--verbose", action="store_true",
                        help="print each request, response, and passing assertion")
    args = parser.parse_args()
    verbose = args.verbose

    repo = Path(__file__).parent.parent
    binary = args.binary

    if not binary:
        print("Building paras binary...")
        binary = str(repo / "paras-test-bin")
        result = subprocess.run(
            ["go", "build", "-o", binary, "./cmd/paras/"],
            cwd=repo, capture_output=True, text=True,
        )
        if result.returncode != 0:
            print(f"Build failed:\n{result.stderr}")
            sys.exit(1)
        print("Build OK\n")

    try:
        with tempfile.TemporaryDirectory(prefix="paras-test-") as vault:
            c = MCPClient(binary, vault=vault)
            try:
                print("Initializing MCP session...")
                init_resp = c.initialize()
                server_info = init_resp.get("result", {}).get("serverInfo", {})
                print(f"Connected to {server_info.get('name', '?')} {server_info.get('version', '?')}")

                run_tests(c)
                run_phase2_tests(c, vault)

            finally:
                c.close()

        run_federation_tests(binary)
        run_promotion_approval_tests(binary)

    finally:
        if not args.binary:
            try:
                os.unlink(binary)
            except FileNotFoundError:
                pass

    _flush_section()
    print()
    if failures == 0:
        print(f"{PASS} All assertions passed")
    else:
        print(f"{FAIL} {failures} assertion(s) failed")
        sys.exit(1)


if __name__ == "__main__":
    main()
