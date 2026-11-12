package ssh

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"encoding/pem"
	"io"
	"log"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
)

func TestBuildHostDocument_NoneWhenEmpty(t *testing.T) {
	if doc := buildHostDocument("h", []byte("  \n"), ""); doc != nil {
		t.Fatalf("expected nil, got %+v", doc)
	}
}

func TestBuildHostDocument_Content(t *testing.T) {
	doc := buildHostDocument("db-1", []byte("# db-1\n\n## Do not\n- x\n"), "")
	if doc == nil {
		t.Fatal("expected a document")
	}
	if doc.Content != "# db-1\n\n## Do not\n- x" {
		t.Errorf("content = %q", doc.Content)
	}
	if doc.Source != "/etc/agents on db-1" {
		t.Errorf("source = %q", doc.Source)
	}
	if doc.Truncated || len(doc.SkippedFiles) != 0 {
		t.Errorf("unexpected flags: %+v", doc)
	}
	if !strings.Contains(doc.Note, "grants no permissions") {
		t.Errorf("note does not state the authority rule: %q", doc.Note)
	}
}

func TestBuildHostDocument_SkippedFilesOnly(t *testing.T) {
	doc := buildHostDocument("h", nil, "skipped: /etc/agents\nnoise\nskipped: /etc/agents.d/10-a.md\n")
	if doc == nil {
		t.Fatal("expected a document that reports the skipped files")
	}
	if doc.Content != "" {
		t.Errorf("content = %q", doc.Content)
	}
	want := []string{"/etc/agents", "/etc/agents.d/10-a.md"}
	if strings.Join(doc.SkippedFiles, ",") != strings.Join(want, ",") {
		t.Errorf("skipped = %v, want %v", doc.SkippedFiles, want)
	}
}

func TestBuildHostDocument_TruncatesOnRuneBoundary(t *testing.T) {
	// "é" is two bytes; an odd limit would cut one in the middle.
	in := bytes.Repeat([]byte("é"), hostDocumentMaxBytes)
	in = append([]byte("a"), in...)
	doc := buildHostDocument("h", in, "")
	if doc == nil || !doc.Truncated {
		t.Fatalf("expected a truncated document, got %+v", doc)
	}
	if len(doc.Content) > hostDocumentMaxBytes {
		t.Errorf("content has %d bytes, limit %d", len(doc.Content), hostDocumentMaxBytes)
	}
	if strings.ContainsRune(doc.Content, '�') {
		t.Error("content ends in a broken UTF-8 sequence")
	}
	if !strings.HasPrefix(doc.Content, "aé") {
		t.Error("truncation must keep the start of the document")
	}
}

func TestLimitedBuffer(t *testing.T) {
	b := &limitedBuffer{max: 5}
	for _, chunk := range []string{"abc", "defg", "hij"} {
		if n, err := b.Write([]byte(chunk)); n != len(chunk) || err != nil {
			t.Fatalf("Write(%q) = %d, %v", chunk, n, err)
		}
	}
	if got := b.buf.String(); got != "abcde" {
		t.Errorf("buffer = %q, want %q", got, "abcde")
	}
}

func TestHostDocumentTracker(t *testing.T) {
	var tr hostDocumentTracker
	now := time.Now()
	host := &SSHHostConfig{Address: "10.0.0.1", User: "root", Port: 22}
	other := &SSHHostConfig{Address: "10.0.0.2", User: "root", Port: 22}
	key := hostDocumentKey("inc-1", host)

	if tr.wasRead(key, now) {
		t.Fatal("new tracker reports a read")
	}
	tr.markRead(key, now)
	if !tr.wasRead(key, now.Add(time.Minute)) {
		t.Error("read not remembered")
	}
	if tr.wasRead(hostDocumentKey("inc-2", host), now) {
		t.Error("another incident must read the document again")
	}
	if tr.wasRead(hostDocumentKey("inc-1", other), now) {
		t.Error("another host must not count as read")
	}
	if tr.wasRead(key, now.Add(hostDocumentTTL)) {
		t.Error("read must expire after the TTL")
	}
	// markRead removes expired entries.
	tr.markRead(hostDocumentKey("inc-3", host), now.Add(hostDocumentTTL+time.Minute))
	if len(tr.seen) != 1 {
		t.Errorf("expired entry not removed: %d entries", len(tr.seen))
	}
}

// writeHostFiles creates a host document tree below a temporary root.
func writeHostFiles(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	for _, d := range []string{"etc/agents.d", "run/agents.d", "usr/lib/agents.d"} {
		if err := os.MkdirAll(filepath.Join(root, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(root, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func runHostDocumentScript(t *testing.T, root string) (string, string) {
	t.Helper()
	cmd := exec.Command("sh", "-s")
	cmd.Stdin = strings.NewReader(hostDocumentScript(root))
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("script failed: %v\nstderr: %s", err, stderr.String())
	}
	return stdout.String(), stderr.String()
}

func TestHostDocumentScript_NoFiles(t *testing.T) {
	stdout, stderr := runHostDocumentScript(t, t.TempDir())
	if stdout != "" || stderr != "" {
		t.Errorf("expected no output, got stdout=%q stderr=%q", stdout, stderr)
	}
}

// The trust check needs files owned by uid 0. As a normal user the test
// files are not, so the script must skip all of them and report them.
func TestHostDocumentScript_SkipsFilesNotOwnedByRoot(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("runs only as a normal user")
	}
	root := writeHostFiles(t, map[string]string{
		"etc/agents":               "MAIN\n",
		"etc/agents.d/10-a.md":     "A\n",
		"usr/lib/agents.d/20-b.md": "B\n",
		"etc/agents.d/readme.txt":  "ignored\n",
	})
	if err := os.Symlink("/dev/null", filepath.Join(root, "etc/agents.d/20-b.md")); err != nil {
		t.Fatal(err)
	}
	stdout, stderr := runHostDocumentScript(t, root)
	if stdout != "" {
		t.Errorf("untrusted content was printed: %q", stdout)
	}
	want := "skipped: /etc/agents\nskipped: /etc/agents.d/10-a.md\n"
	if stderr != want {
		t.Errorf("stderr = %q, want %q", stderr, want)
	}
}

func TestHostDocumentScript_AsRoot(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("needs uid 0 (run in a container or a user namespace)")
	}
	root := writeHostFiles(t, map[string]string{
		"etc/agents":                  "MAIN\n",
		"usr/lib/agents.d/50-pg.md":   "pg vendor\n",
		"etc/agents.d/50-pg.md":       "pg site\n",
		"run/agents.d/10-maint.md":    "maintenance\n",
		"usr/lib/agents.d/60-ng.md":   "masked\n",
		"usr/lib/agents.d/70-rd.md":   "vendor only\n",
		"etc/agents.d/80-open.md":     "writable by all\n",
		"usr/lib/agents.d/80-open.md": "must stay masked\n",
		"etc/agents.d/notes.txt":      "ignored\n",
	})
	if err := os.Symlink("/dev/null", filepath.Join(root, "etc/agents.d/60-ng.md")); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(filepath.Join(root, "etc/agents.d/80-open.md"), 0o666); err != nil {
		t.Fatal(err)
	}
	stdout, stderr := runHostDocumentScript(t, root)
	wantOut := "MAIN\n\nmaintenance\n\npg site\n\nvendor only\n"
	if stdout != wantOut {
		t.Errorf("stdout = %q, want %q", stdout, wantOut)
	}
	if stderr != "skipped: /etc/agents.d/80-open.md\n" {
		t.Errorf("stderr = %q", stderr)
	}
}

// startTestSSHServer runs an SSH server on localhost that executes each
// command with the local sh. It returns the address and a client key.
func startTestSSHServer(t *testing.T) (addr string, clientKeyPEM string) {
	t.Helper()
	_, hostPriv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	hostSigner, err := ssh.NewSignerFromKey(hostPriv)
	if err != nil {
		t.Fatal(err)
	}
	_, clientPriv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	block, err := ssh.MarshalPrivateKey(clientPriv, "")
	if err != nil {
		t.Fatal(err)
	}

	serverConfig := &ssh.ServerConfig{
		PublicKeyCallback: func(ssh.ConnMetadata, ssh.PublicKey) (*ssh.Permissions, error) {
			return nil, nil
		},
	}
	serverConfig.AddHostKey(hostSigner)

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { listener.Close() })

	go func() {
		for {
			nc, err := listener.Accept()
			if err != nil {
				return
			}
			go serveTestSSHConn(nc, serverConfig)
		}
	}()
	return listener.Addr().String(), string(pem.EncodeToMemory(block))
}

func serveTestSSHConn(nc net.Conn, config *ssh.ServerConfig) {
	conn, chans, reqs, err := ssh.NewServerConn(nc, config)
	if err != nil {
		return
	}
	defer conn.Close()
	go ssh.DiscardRequests(reqs)
	for newChan := range chans {
		if newChan.ChannelType() != "session" {
			newChan.Reject(ssh.UnknownChannelType, "session only")
			continue
		}
		ch, chReqs, err := newChan.Accept()
		if err != nil {
			continue
		}
		go func() {
			defer ch.Close()
			for req := range chReqs {
				if req.Type != "exec" {
					req.Reply(false, nil)
					continue
				}
				var payload struct{ Command string }
				ssh.Unmarshal(req.Payload, &payload)
				req.Reply(true, nil)

				cmd := exec.Command("sh", "-c", payload.Command)
				cmd.Stdin = ch
				cmd.Stdout = ch
				cmd.Stderr = ch.Stderr()
				status := uint32(0)
				if err := cmd.Run(); err != nil {
					status = 1
					if exitErr, ok := err.(*exec.ExitError); ok {
						status = uint32(exitErr.ExitCode())
					}
				}
				ch.SendRequest("exit-status", false, ssh.Marshal(struct{ Status uint32 }{status}))
				return
			}
		}()
	}
}

func TestExecuteOnServer_HostDocument(t *testing.T) {
	addr, clientKey := startTestSSHServer(t)
	host, portStr, _ := net.SplitHostPort(addr)
	port, _ := net.LookupPort("tcp", portStr)

	root := writeHostFiles(t, map[string]string{
		"etc/agents":           "# test-host\n\n## Do not\n- Do not restart the database.\n",
		"etc/agents.d/10-a.md": "## Site\nChange freeze.\n",
	})
	marker := filepath.Join(t.TempDir(), "marker")

	tool := NewSSHTool(log.New(io.Discard, "", 0))
	tool.hostDocRoot = root
	hostConfig := &SSHHostConfig{Hostname: "test-host", Address: host, User: "root", Port: port, AllowWriteCommands: true}
	config := &SSHConfig{
		Keys:              map[string]*SSHKey{"k": {ID: "k", PrivateKey: clientKey, IsDefault: true}},
		DefaultKeyID:      "k",
		CommandTimeout:    20,
		ConnectionTimeout: 10,
		ReadHostDocument:  true,
	}
	ctx := context.Background()
	asRoot := os.Geteuid() == 0

	// 1. First command, read-only: it runs and the document comes with it.
	first := tool.executeOnServer(ctx, "inc-1", hostConfig, "echo hello", config)
	if !first.Success || strings.TrimSpace(first.Stdout) != "hello" {
		t.Fatalf("first command failed: %+v", first)
	}
	if first.HostDocument == nil {
		t.Fatal("first command did not return the host document")
	}
	if asRoot {
		want := "# test-host\n\n## Do not\n- Do not restart the database.\n\n## Site\nChange freeze."
		if first.HostDocument.Content != want {
			t.Errorf("content = %q, want %q", first.HostDocument.Content, want)
		}
	} else {
		// Test files are not owned by root: they must be skipped and reported.
		if first.HostDocument.Content != "" {
			t.Errorf("untrusted files were used: %q", first.HostDocument.Content)
		}
		if got := strings.Join(first.HostDocument.SkippedFiles, ","); got != "/etc/agents,/etc/agents.d/10-a.md" {
			t.Errorf("skipped = %q", got)
		}
	}
	raw, _ := json.Marshal(first)
	if !strings.Contains(string(raw), `"host_document"`) {
		t.Errorf("host_document missing from JSON: %s", raw)
	}

	// 2. Second command of the same incident: no document again.
	second := tool.executeOnServer(ctx, "inc-1", hostConfig, "echo again", config)
	if !second.Success || second.HostDocument != nil {
		t.Errorf("second command: %+v", second)
	}
	raw, _ = json.Marshal(second)
	if strings.Contains(string(raw), "host_document") {
		t.Errorf("host_document must be omitted: %s", raw)
	}

	// 3. Another incident whose first command is a write command.
	write := "touch " + marker
	held := tool.executeOnServer(ctx, "inc-2", hostConfig, write, config)
	_, statErr := os.Stat(marker)
	if asRoot {
		// A trusted document exists: the write is held once.
		if held.Success || held.Error != hostDocumentWriteHold || held.HostDocument == nil {
			t.Errorf("write command was not held: %+v", held)
		}
		if statErr == nil {
			t.Error("held command was executed")
		}
		retry := tool.executeOnServer(ctx, "inc-2", hostConfig, write, config)
		if !retry.Success || retry.HostDocument != nil {
			t.Errorf("retry after hold failed: %+v", retry)
		}
		if _, err := os.Stat(marker); err != nil {
			t.Error("retry did not execute the command")
		}
	} else {
		// No trusted content: nothing to read first, the command runs.
		if !held.Success || statErr != nil {
			t.Errorf("write command must run when there is no trusted document: %+v", held)
		}
	}

	// 4. Setting off: no document, no hold.
	config.ReadHostDocument = false
	off := tool.executeOnServer(ctx, "inc-3", hostConfig, "echo off", config)
	if !off.Success || off.HostDocument != nil {
		t.Errorf("setting off: %+v", off)
	}
}

func TestExecuteOnServer_NoHostDocument(t *testing.T) {
	addr, clientKey := startTestSSHServer(t)
	host, portStr, _ := net.SplitHostPort(addr)
	port, _ := net.LookupPort("tcp", portStr)

	tool := NewSSHTool(log.New(io.Discard, "", 0))
	tool.hostDocRoot = t.TempDir() // empty tree
	marker := filepath.Join(t.TempDir(), "marker")
	hostConfig := &SSHHostConfig{Hostname: "bare", Address: host, User: "root", Port: port, AllowWriteCommands: true}
	config := &SSHConfig{
		Keys:              map[string]*SSHKey{"k": {ID: "k", PrivateKey: clientKey, IsDefault: true}},
		DefaultKeyID:      "k",
		CommandTimeout:    20,
		ConnectionTimeout: 10,
		ReadHostDocument:  true,
	}
	// A host without /etc/agents behaves as before: a first write command runs.
	res := tool.executeOnServer(context.Background(), "inc-1", hostConfig, "touch "+marker, config)
	if !res.Success || res.HostDocument != nil {
		t.Fatalf("unexpected result: %+v", res)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Error("command was not executed")
	}
}
