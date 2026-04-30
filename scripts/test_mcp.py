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


class MCPClient:
    def __init__(self, binary: str, vault: str):
        self.proc = subprocess.Popen(
            [binary, "--vault", vault],
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
    })
    is_err, text = check_ok("create projects/hello.md", resp)
    etag_hello = None
    if not is_err:
        data = parse_json(text)
        check("title set", data and data.get("Title") == "Hello World", str(data))
        check("tags normalized", data and "aws" in data.get("Tags", []), str(data))
        check("tags normalized #Cloud→cloud", data and "cloud" in data.get("Tags", []), str(data))
        check("ETag present", data and bool(data.get("ETag")), str(data))
        etag_hello = data.get("ETag") if data else None

    resp = c.call("note_create", {"path": "projects/hello.md"})
    check_err("duplicate create → conflict", resp, "conflict")

    resp = c.call("note_create", {"path": "notes/foo.md"})
    check_err("non-PARA root → error", resp)

    resp = c.call("note_create", {"path": "../etc/passwd"})
    check_err("path traversal → error", resp)

    section("note_get")
    resp = c.call("note_get", {"scope": "personal", "path": "projects/hello.md"})
    is_err, text = check_ok("get projects/hello.md", resp)
    if not is_err:
        data = parse_json(text)
        check("body round-trips", data and "vpc configuration" in data.get("Body", ""), str(data))
        check("ETag matches create", data and data.get("ETag") == etag_hello, str(data))

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
        check("ETag rotated", data and data.get("ETag") != etag_hello, str(data))
        etag_hello2 = data.get("ETag") if data else None

    resp = c.call("note_update_body", {
        "scope": "personal",
        "path": "projects/hello.md",
        "body": "This should fail.",
        "if_match": etag_hello,  # stale
    })
    check_err("stale ETag → conflict", resp, "conflict")

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
        etag_hello3 = data.get("ETag") if data else None

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

    section("notes_list")
    c.call("note_create", {"path": "resources/guide.md", "body": "resource content", "tags": ["aws"]})
    c.call("note_create", {"path": "projects/second.md", "status": "active"})

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

    resp = c.call("notes_list", {"limit": 1, "sort": "updated_at"})
    is_err, text = check_ok("list with limit=1", resp)
    if not is_err:
        data = parse_json(text)
        check("exactly 1 result", data and len(data.get("Notes", [])) == 1, str(data))
        check("has_more when total > 1", data and data.get("Total", 0) > 1, str(data))

    section("notes_search")
    c.call("note_create", {"path": "resources/searchable.md",
                           "body": "unique_searchterm_xk9 about distributed systems"})
    time.sleep(150 / 1000)  # let the BM25 writer goroutine publish the snapshot

    resp = c.call("notes_search", {"text": "unique_searchterm_xk9", "limit": 5})
    is_err, text = check_ok("search for unique term", resp)
    if not is_err:
        results = parse_json(text)
        check("found at least one result", results is not None and len(results) > 0, str(results))
        if results:
            check("top result has Score", results[0].get("Score", 0) > 0, str(results[0]))

    resp = c.call("notes_search", {"text": "xyzzy_nonexistent_term_8675309"})
    is_err, text = check_ok("search with no matches", resp)
    if not is_err:
        results = parse_json(text)
        check("empty results for unknown term", results is not None and len(results) == 0, str(results))

    section("scope_resolver")
    # The server wires AllowedScopes from a server-side ScopesFunc, never from
    # wire input. These tests verify that the resolver is active and correct:
    # notes in the personal vault are visible, and the scope string supplied
    # by the caller in NoteRef is validated (must match the vault's scope).

    # Scope field in NoteRef must be "personal" for the single-vault build.
    resp = c.call("note_get", {"scope": "wrong-scope", "path": "areas/hello.md"})
    check_err("wrong scope on note_get → error", resp)

    # notes_list returns notes because AllowedScopes = ["personal"] includes the vault.
    resp = c.call("notes_list", {})
    is_err, text = check_ok("notes_list succeeds (scopes resolver active)", resp)
    if not is_err:
        data = parse_json(text)
        check("notes_list returns non-empty result", data and data.get("Total", 0) > 0, str(data))

    # notes_search returns results because AllowedScopes includes personal vault.
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
        check("ByCategory present", data and "ByCategory" in data, str(data))

    section("note_delete")
    resp = c.call("note_create", {"path": "projects/soft-del.md", "body": "bye"})

    resp = c.call("note_delete", {"scope": "personal", "path": "projects/soft-del.md", "soft": True})
    check_ok("soft delete", resp)

    resp = c.call("note_get", {"scope": "personal", "path": "projects/soft-del.md"})
    check_err("soft-deleted note not accessible", resp, "not_found")

    c.call("note_create", {"path": "projects/hard-del.md", "body": "gone"})
    resp = c.call("note_delete", {"scope": "personal", "path": "projects/hard-del.md", "soft": False})
    check_ok("hard delete", resp)

    resp = c.call("note_get", {"scope": "personal", "path": "projects/hard-del.md"})
    check_err("hard-deleted note not accessible", resp, "not_found")


def run_phase2_tests(c: MCPClient):

    section("vault_health")
    resp = c.call("vault_health", {})
    is_err, text = check_ok("vault_health succeeds", resp)
    if not is_err:
        data = parse_json(text)
        check("UnrecognizedFiles present", data is not None and "UnrecognizedFiles" in data, str(data))
        check("WatcherStatus present", data is not None and "WatcherStatus" in data, str(data))

    section("vault_rescan")
    resp = c.call("vault_rescan", {})
    is_err, text = check_ok("vault_rescan succeeds", resp)
    if not is_err:
        check("returns rescan complete", "rescan" in text.lower(), text)

    section("notes_stale")
    # All notes in the vault were just created — none should be stale with days=1
    resp = c.call("notes_stale", {"days": 1})
    is_err, text = check_ok("notes_stale with days=1", resp)
    if not is_err:
        data = parse_json(text)
        check("returns Notes key", data is not None and "Notes" in data, str(data))
        check("no notes stale within 1 day", data and len(data.get("Notes", [])) == 0, str(data))

    # notes_stale with days=0 should error
    resp = c.call("notes_stale", {"days": 0})
    check_err("days=0 → error", resp)

    section("notes_backlinks")
    # Create two notes that link to a target note, one via asset embed
    c.call("note_create", {"path": "projects/target.md", "title": "Target Note", "body": "I am the target."})
    c.call("note_create", {
        "path": "projects/linker-a.md",
        "body": "See [[target]] for details.",
    })
    c.call("note_create", {
        "path": "resources/linker-b.md",
        "body": "Reference ![[target]] as an asset.",
    })
    c.call("note_create", {
        "path": "areas/unrelated.md",
        "body": "No links here.",
    })

    # Default (include_assets=false) should only return linker-a
    resp = c.call("notes_backlinks", {"scope": "personal", "path": "projects/target.md"})
    is_err, text = check_ok("backlinks (no assets)", resp)
    if not is_err:
        data = parse_json(text)
        check("returns list", isinstance(data, list), str(data))
        paths = [e.get("Summary", {}).get("Ref", {}).get("Path", "") for e in (data or [])]
        check("linker-a included", any("linker-a" in p for p in paths), str(paths))
        check("linker-b excluded (asset)", not any("linker-b" in p for p in paths), str(paths))
        check("unrelated excluded", not any("unrelated" in p for p in paths), str(paths))

    # With include_assets=true both linkers should appear; linker-b has IsAsset=true
    resp = c.call("notes_backlinks", {"scope": "personal", "path": "projects/target.md", "include_assets": True})
    is_err, text = check_ok("backlinks (include_assets=true)", resp)
    if not is_err:
        data = parse_json(text)
        check("both linkers present", data is not None and len(data) >= 2, str(data))
        asset_entries = [e for e in (data or []) if e.get("IsAsset")]
        non_asset_entries = [e for e in (data or []) if not e.get("IsAsset")]
        check("linker-b has IsAsset=true", len(asset_entries) >= 1, str(data))
        check("linker-a has IsAsset=false", len(non_asset_entries) >= 1, str(data))

    # Backlinks for a note with no inbound links should return empty array
    resp = c.call("notes_backlinks", {"scope": "personal", "path": "areas/unrelated.md"})
    is_err, text = check_ok("backlinks for note with no inbound links", resp)
    if not is_err:
        data = parse_json(text)
        check("empty array", data == [], str(data))

    section("notes_related")
    c.call("note_create", {
        "path": "projects/rel-a.md",
        "body": "Project A",
        "tags": ["go", "backend"],
        "status": "active",
    })
    c.call("note_create", {
        "path": "projects/rel-b.md",
        "body": "Project B shares tags",
        "tags": ["go", "backend"],
        "status": "active",
    })
    c.call("note_create", {
        "path": "resources/rel-c.md",
        "body": "Unrelated resource",
        "tags": ["cooking"],
    })

    resp = c.call("notes_related", {"scope": "personal", "path": "projects/rel-a.md"})
    is_err, text = check_ok("notes_related for rel-a", resp)
    if not is_err:
        data = parse_json(text)
        check("returns list", isinstance(data, list), str(data))
        if data:
            check("top result has Score > 0", data[0].get("Score", 0) > 0, str(data[0]))
            top_path = data[0].get("Summary", {}).get("Ref", {}).get("Path", "")
            check("top result is rel-b (most overlap)", "rel-b" in top_path, str(data))

    # Related for note with no shared attributes should return empty or low-scored
    resp = c.call("notes_related", {"scope": "personal", "path": "resources/rel-c.md"})
    is_err, text = check_ok("notes_related with no overlap", resp)
    if not is_err:
        data = parse_json(text)
        check("returns list", isinstance(data, list), str(data))

    section("notes_create_batch")
    resp = c.call("notes_create_batch", {
        "notes": [
            {"path": "projects/batch-a.md", "body": "Batch note A", "status": "active"},
            {"path": "projects/batch-b.md", "body": "Batch note B"},
            {"path": "../escape/bad.md", "body": "should fail"},  # path traversal
        ]
    })
    is_err, text = check_ok("notes_create_batch with mixed success/failure", resp)
    if not is_err:
        data = parse_json(text)
        check("SuccessCount == 2", data and data.get("SuccessCount") == 2, str(data))
        check("FailureCount == 1", data and data.get("FailureCount") == 1, str(data))
        results = data.get("Results", []) if data else []
        check("3 results returned", len(results) == 3, str(results))
        ok_results = [r for r in results if r.get("OK")]
        fail_results = [r for r in results if not r.get("OK")]
        check("2 OK results", len(ok_results) == 2, str(results))
        check("1 failed result has Error", len(fail_results) == 1 and fail_results[0].get("Error"), str(fail_results))

    section("notes_update_batch")
    resp = c.call("notes_update_batch", {
        "notes": [
            {"path": "projects/batch-a.md", "body": "Updated body A"},
            {"path": "projects/batch-b.md", "body": "Updated body B"},
            {"path": "projects/nonexistent.md", "body": "should fail"},
        ]
    })
    is_err, text = check_ok("notes_update_batch with mixed success/failure", resp)
    if not is_err:
        data = parse_json(text)
        check("SuccessCount == 2", data and data.get("SuccessCount") == 2, str(data))
        check("FailureCount == 1", data and data.get("FailureCount") == 1, str(data))

    section("notes_patch_frontmatter_batch")
    resp = c.call("notes_patch_frontmatter_batch", {
        "notes": [
            {"path": "projects/batch-a.md", "fields": {"status": "done"}},
            {"path": "projects/batch-b.md", "fields": {"status": "done"}},
            {"path": "projects/nonexistent.md", "fields": {"status": "done"}},
        ]
    })
    is_err, text = check_ok("notes_patch_frontmatter_batch with mixed success/failure", resp)
    if not is_err:
        data = parse_json(text)
        check("SuccessCount == 2", data and data.get("SuccessCount") == 2, str(data))
        check("FailureCount == 1", data and data.get("FailureCount") == 1, str(data))
        # verify status was actually updated
    resp = c.call("note_get", {"scope": "personal", "path": "projects/batch-a.md"})
    is_err, text = check_ok("get batch-a after patch", resp)
    if not is_err:
        data = parse_json(text)
        check("status=done applied", data and data.get("FrontMatter", {}).get("Status") == "done", str(data))

    section("category_templates")
    # Projects notes created without explicit status should get status=active from default template
    resp = c.call("note_create", {"path": "projects/templated.md", "title": "Templated Project"})
    is_err, text = check_ok("create note in projects/ (template applies)", resp)
    if not is_err:
        data = parse_json(text)
        check("status=active from template", data and data.get("Status") == "active", str(data))

    # Archives notes should get status=archived from default template
    resp = c.call("note_create", {"path": "archives/templated-archive.md", "title": "Archived"})
    is_err, text = check_ok("create note in archives/ (template applies)", resp)
    if not is_err:
        data = parse_json(text)
        check("status=archived from template", data and data.get("Status") == "archived", str(data))

    # Explicit status should override the template
    resp = c.call("note_create", {"path": "projects/explicit-status.md", "status": "paused"})
    is_err, text = check_ok("explicit status overrides template", resp)
    if not is_err:
        data = parse_json(text)
        check("status=paused (explicit wins)", data and data.get("Status") == "paused", str(data))


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

    with tempfile.TemporaryDirectory(prefix="paras-test-") as vault:
        c = MCPClient(binary, vault)
        try:
            print("Initializing MCP session...")
            init_resp = c.initialize()
            server_info = init_resp.get("result", {}).get("serverInfo", {})
            print(f"Connected to {server_info.get('name', '?')} {server_info.get('version', '?')}")

            run_tests(c)
            run_phase2_tests(c)

        finally:
            c.close()
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
