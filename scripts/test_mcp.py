#!/usr/bin/env python3
"""End-to-end test for the paras MCP server.

Builds the binary, starts it against a temp vault, runs all 10 tools through
the MCP stdio transport, and reports pass/fail for each assertion.

Usage:
    python3 scripts/test_mcp.py
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
SKIP = "\033[33m-\033[0m"

_id = 0
failures = 0


def next_id():
    global _id
    _id += 1
    return _id


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
        # Drain any extra notifications before sending initialized
        self._send({
            "jsonrpc": "2.0",
            "method": "notifications/initialized",
            "params": {},
        })
        return resp

    def call(self, tool: str, arguments: dict) -> dict:
        rid = next_id()
        self._send({
            "jsonrpc": "2.0",
            "id": rid,
            "method": "tools/call",
            "params": {"name": tool, "arguments": arguments},
        })
        while True:
            resp = self._recv()
            # Skip notifications
            if "method" in resp and "id" not in resp:
                continue
            if resp.get("id") == rid:
                return resp

    def close(self):
        try:
            self.proc.stdin.close()
        except Exception:
            pass
        self.proc.wait(timeout=5)


def result_content(resp: dict) -> tuple[bool, str]:
    """Returns (is_error, text) from a tools/call response."""
    if "error" in resp:
        return True, str(resp["error"])
    result = resp.get("result", {})
    content = result.get("content", [])
    is_error = result.get("isError", False)
    text = content[0].get("text", "") if content else ""
    return is_error, text


def parse_json(text: str) -> dict | list | None:
    try:
        return json.loads(text)
    except json.JSONDecodeError:
        return None


# --- assertion helpers ---

def check(label: str, cond: bool, detail: str = ""):
    global failures
    if cond:
        print(f"  {PASS} {label}")
    else:
        print(f"  {FAIL} {label}" + (f": {detail}" if detail else ""))
        failures += 1


def check_ok(label: str, resp: dict) -> tuple[bool, str]:
    is_err, text = result_content(resp)
    check(label + " (no error)", not is_err, text if is_err else "")
    return is_err, text


def check_err(label: str, resp: dict, contains: str = "") -> str:
    is_err, text = result_content(resp)
    check(label + " (returns error)", is_err, text if not is_err else "")
    if contains:
        check(label + f" (error contains {contains!r})", contains in text, text)
    return text


# ============================================================
# Tests
# ============================================================

def run_tests(c: MCPClient):

    # ── note_create ──────────────────────────────────────────
    print("\n[note_create]")
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

    # duplicate create → conflict
    resp = c.call("note_create", {"path": "projects/hello.md"})
    check_err("duplicate create → conflict", resp, "conflict")

    # invalid path (no PARA root)
    resp = c.call("note_create", {"path": "notes/foo.md"})
    check_err("non-PARA root → error", resp)

    # path traversal
    resp = c.call("note_create", {"path": "../etc/passwd"})
    check_err("path traversal → error", resp)

    # ── note_get ─────────────────────────────────────────────
    print("\n[note_get]")
    resp = c.call("note_get", {"scope": "personal", "path": "projects/hello.md"})
    is_err, text = check_ok("get projects/hello.md", resp)
    if not is_err:
        data = parse_json(text)
        check("body round-trips", data and "vpc configuration" in data.get("Body", ""), str(data))
        check("ETag matches create", data and data.get("ETag") == etag_hello, str(data))

    # get non-existent
    resp = c.call("note_get", {"scope": "personal", "path": "projects/nope.md"})
    check_err("get missing → not_found", resp, "not_found")

    # ── note_update_body ─────────────────────────────────────
    print("\n[note_update_body]")
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

    # stale ETag → conflict
    resp = c.call("note_update_body", {
        "scope": "personal",
        "path": "projects/hello.md",
        "body": "This should fail.",
        "if_match": etag_hello,  # stale
    })
    check_err("stale ETag → conflict", resp, "conflict")

    # ── note_patch_frontmatter ───────────────────────────────
    print("\n[note_patch_frontmatter]")
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

    # ── note_move ────────────────────────────────────────────
    print("\n[note_move]")
    resp = c.call("note_move", {
        "scope": "personal",
        "path": "projects/hello.md",
        "new_path": "areas/hello.md",
        "if_match": etag_hello3,
    })
    is_err, text = check_ok("move to areas/", resp)
    etag_moved = None
    if not is_err:
        data = parse_json(text)
        check("new path in ref", data and data.get("Ref", {}).get("Path") == "areas/hello.md", str(data))
        etag_moved = data.get("ETag") if data else None

    # old path gone
    resp = c.call("note_get", {"scope": "personal", "path": "projects/hello.md"})
    check_err("old path gone after move", resp, "not_found")

    # new path accessible
    resp = c.call("note_get", {"scope": "personal", "path": "areas/hello.md"})
    check_ok("new path accessible after move", resp)

    # ── note_archive ─────────────────────────────────────────
    print("\n[note_archive]")
    # Create a note to archive
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

    # ── notes_list ───────────────────────────────────────────
    print("\n[notes_list]")
    # Create a few more notes
    c.call("note_create", {"path": "resources/guide.md", "body": "resource content", "tags": ["aws"]})
    c.call("note_create", {"path": "projects/second.md", "status": "active"})

    resp = c.call("notes_list", {})
    is_err, text = check_ok("list all notes", resp)
    if not is_err:
        data = parse_json(text)
        check("returns notes", data and len(data.get("Notes", [])) > 0, str(data))
        check("total > 0", data and data.get("Total", 0) > 0, str(data))

    # filter by status
    resp = c.call("notes_list", {"status": "active"})
    is_err, text = check_ok("list by status=active", resp)
    if not is_err:
        data = parse_json(text)
        notes = data.get("Notes", []) if data else []
        check("all returned notes are active",
              all(n.get("Status") == "active" for n in notes),
              str(notes))

    # filter by category
    resp = c.call("notes_list", {"categories": ["projects"]})
    is_err, text = check_ok("list projects only", resp)
    if not is_err:
        data = parse_json(text)
        notes = data.get("Notes", []) if data else []
        check("all returned notes in projects/",
              all(n.get("Ref", {}).get("Path", "").startswith("projects/") for n in notes),
              str(notes))

    # sort and limit
    resp = c.call("notes_list", {"limit": 1, "sort": "updated_at"})
    is_err, text = check_ok("list with limit=1", resp)
    if not is_err:
        data = parse_json(text)
        check("exactly 1 result", data and len(data.get("Notes", [])) == 1, str(data))
        check("has_more set when there are more", data and data.get("Total", 0) > 1, str(data))

    # ── notes_search ─────────────────────────────────────────
    print("\n[notes_search]")
    # Create a dedicated search target so the term is definitely indexed.
    c.call("note_create", {"path": "resources/searchable.md", "body": "unique_searchterm_xk9 about distributed systems"})
    time.sleep(150 / 1000)  # let the BM25 writer goroutine publish the snapshot

    resp = c.call("notes_search", {"text": "unique_searchterm_xk9", "limit": 5})
    is_err, text = check_ok("search for unique term", resp)
    if not is_err:
        results = parse_json(text)
        check("found at least one result", results is not None and len(results) > 0, str(results))
        if results:
            check("top result has Score", results[0].get("Score", 0) > 0, str(results[0]))

    # search with no results
    resp = c.call("notes_search", {"text": "xyzzy_nonexistent_term_8675309"})
    is_err, text = check_ok("search with no matches (no error)", resp)
    if not is_err:
        results = parse_json(text)
        check("empty results for unknown term", results is not None and len(results) == 0, str(results))

    # ── vault_stats ──────────────────────────────────────────
    print("\n[vault_stats]")
    resp = c.call("vault_stats", {})
    is_err, text = check_ok("vault_stats", resp)
    if not is_err:
        data = parse_json(text)
        check("TotalNotes > 0", data and data.get("TotalNotes", 0) > 0, str(data))
        check("ByCategory present", data and "ByCategory" in data, str(data))

    # ── note_delete (soft) ───────────────────────────────────
    print("\n[note_delete]")
    resp = c.call("note_create", {"path": "projects/soft-del.md", "body": "bye"})
    _, text = result_content(resp)
    etag_del = parse_json(text).get("ETag") if not result_content(resp)[0] else None

    resp = c.call("note_delete", {
        "scope": "personal",
        "path": "projects/soft-del.md",
        "soft": True,
    })
    check_ok("soft delete", resp)

    resp = c.call("note_get", {"scope": "personal", "path": "projects/soft-del.md"})
    check_err("soft-deleted note not accessible via get", resp, "not_found")

    # hard delete
    c.call("note_create", {"path": "projects/hard-del.md", "body": "gone"})
    resp = c.call("note_delete", {
        "scope": "personal",
        "path": "projects/hard-del.md",
        "soft": False,
    })
    check_ok("hard delete", resp)

    resp = c.call("note_get", {"scope": "personal", "path": "projects/hard-del.md"})
    check_err("hard-deleted note not accessible via get", resp, "not_found")


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--binary", default="", help="path to paras binary")
    args = parser.parse_args()

    repo = Path(__file__).parent.parent
    binary = args.binary

    if not binary:
        print("Building paras binary...")
        binary = str(repo / "paras-test-bin")
        result = subprocess.run(
            ["go", "build", "-o", binary, "./cmd/paras/"],
            cwd=repo,
            capture_output=True,
            text=True,
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
            print(f"Connected to {server_info.get('name', '?')} {server_info.get('version', '?')}\n")

            run_tests(c)

        finally:
            c.close()
            # Clean up build artifact
            if not args.binary:
                try:
                    os.unlink(binary)
                except FileNotFoundError:
                    pass

    print()
    total = _id - 1  # rough proxy
    if failures == 0:
        print(f"{PASS} All assertions passed")
    else:
        print(f"{FAIL} {failures} assertion(s) failed")
        sys.exit(1)


if __name__ == "__main__":
    main()
