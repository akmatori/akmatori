// Inject a POST /update route into QMD's MCP HTTP server (dist/mcp/server.js).
//
// The akmatori API server POSTs to http://qmd:8181/update after every runbook
// CRUD to keep the search index current. Upstream QMD doesn't expose this
// route, so without this patch every call returns 404 and the index drifts.
//
// We anchor the insertion immediately before the existing "/health" handler.
// The injected handler shells out via execFile("qmd", ["update"]) — fixed
// argv, no shell, no user input.

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
    `${indent}    execFile("qmd", ["update"], { timeout: 300000 }, (err, stdout, stderr) => {\n` +
    `${indent}        if (err) {\n` +
    `${indent}            nodeRes.writeHead(500, { "Content-Type": "application/json" });\n` +
    `${indent}            nodeRes.end(JSON.stringify({ error: String(err), stderr }));\n` +
    `${indent}            log(\`\${ts()} POST /update FAILED (\${Date.now() - reqStart}ms)\`);\n` +
    `${indent}            return;\n` +
    `${indent}        }\n` +
    `${indent}        nodeRes.writeHead(200, { "Content-Type": "application/json" });\n` +
    `${indent}        nodeRes.end(JSON.stringify({ status: "ok", output: stdout.trim() }));\n` +
    `${indent}        log(\`\${ts()} POST /update (\${Date.now() - reqStart}ms)\`);\n` +
    `${indent}    });\n` +
    `${indent}    return;\n` +
    `${indent}}\n` +
    `${indent}`;

const patched = src.slice(0, lineStart) + indent + injection + src.slice(idx);
fs.writeFileSync(target, patched);
console.log(`patch-server.js: injected /update route into ${path.basename(target)}`);
