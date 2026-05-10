// Inject a POST /update route into QMD's MCP HTTP server (dist/mcp/server.js).
//
// The akmatori API server POSTs to http://qmd:8181/update after every runbook
// CRUD to keep the search index current. Upstream QMD doesn't expose this
// route, so without this patch every call returns 404 and the index drifts.
//
// Two-stage flow:
//   1) `qmd update` (fast: refreshes BM25 lex index from disk)  — synchronous;
//      success/failure surfaces in the HTTP response so callers can detect
//      fast-path breakage immediately.
//   2) `qmd embed`  (slow: refreshes vector index, can take seconds-to-minutes)
//      — runs in the background AFTER the 200 response is sent. The Go-side
//      clients fire /update from a goroutine and use a short HTTP timeout;
//      embedding inline would cause spurious context-deadline warnings on
//      every CRUD even though the lex index update succeeded.
//
// Embed concurrency: a single in-flight flag (__qmdEmbedInFlight) suppresses
// duplicate concurrent embed processes. qmd embed is ETag-cached and idempotent
// — the running process will pick up any newly-staged docs from a colliding
// /update call, so dropping the duplicate is safe.
//
// We anchor the insertion immediately before the existing "/health" handler.
// The injected handler shells out via execFile("qmd", [...]) — fixed argv,
// no shell, no user input.

const fs = require("node:fs");
const path = require("node:path");

const target = process.argv[2];
if (!target) {
    console.error("usage: node patch-server.js <path-to-server.js>");
    process.exit(2);
}

const src = fs.readFileSync(target, "utf8");

if (src.includes('pathname === "/update"')) {
    console.log(`patch-server.js: /update route already present in ${path.basename(target)}, skipping`);
    process.exit(0);
}

const anchor = 'if (pathname === "/health" && nodeReq.method === "GET") {';
const idx = src.indexOf(anchor);
if (idx === -1) {
    console.error(`patch-server.js: anchor not found in ${target}`);
    console.error("expected literal: " + anchor);
    process.exit(1);
}

// Match the indentation of the anchor line so the injection is properly nested.
const lineStart = src.lastIndexOf("\n", idx) + 1;
const indent = src.slice(lineStart, idx);

const injection =
    `if (pathname === "/update" && nodeReq.method === "POST") {\n` +
    `${indent}    const { execFile } = await import("node:child_process");\n` +
    `${indent}    execFile("qmd", ["update"], { timeout: 300000 }, (updErr, updStdout, updStderr) => {\n` +
    `${indent}        if (updErr) {\n` +
    `${indent}            nodeRes.writeHead(500, { "Content-Type": "application/json" });\n` +
    `${indent}            nodeRes.end(JSON.stringify({ error: String(updErr), stderr: updStderr, stage: "update" }));\n` +
    `${indent}            log(\`\${ts()} POST /update FAILED at update stage (\${Date.now() - reqStart}ms)\`);\n` +
    `${indent}            return;\n` +
    `${indent}        }\n` +
    // Ack the lex-index refresh synchronously, then run the slow vector-index
    // refresh in the background. See file header for the rationale.
    `${indent}        const embedSkipped = globalThis.__qmdEmbedInFlight === true;\n` +
    `${indent}        nodeRes.writeHead(200, { "Content-Type": "application/json" });\n` +
    `${indent}        nodeRes.end(JSON.stringify({ status: "ok", updateOutput: updStdout.trim(), embedQueued: !embedSkipped }));\n` +
    `${indent}        log(\`\${ts()} POST /update update-stage ok (\${Date.now() - reqStart}ms)\${embedSkipped ? "; embed already in flight, skipped" : ""}\`);\n` +
    `${indent}        if (embedSkipped) {\n` +
    `${indent}            return;\n` +
    `${indent}        }\n` +
    `${indent}        globalThis.__qmdEmbedInFlight = true;\n` +
    `${indent}        const embedStart = Date.now();\n` +
    `${indent}        execFile("qmd", ["embed"], { timeout: 600000 }, (embErr, embStdout, embStderr) => {\n` +
    `${indent}            globalThis.__qmdEmbedInFlight = false;\n` +
    `${indent}            if (embErr) {\n` +
    `${indent}                log(\`\${ts()} /update embed-stage FAILED (\${Date.now() - embedStart}ms): \${String(embErr)} stderr=\${String(embStderr || "").trim()}\`);\n` +
    `${indent}                return;\n` +
    `${indent}            }\n` +
    `${indent}            log(\`\${ts()} /update embed-stage ok (\${Date.now() - embedStart}ms)\`);\n` +
    `${indent}        });\n` +
    `${indent}    });\n` +
    `${indent}    return;\n` +
    `${indent}}\n` +
    `${indent}`;

const patched = src.slice(0, lineStart) + indent + injection + src.slice(idx);
fs.writeFileSync(target, patched);
console.log(`patch-server.js: injected /update route into ${path.basename(target)}`);
