/*
Copyright 2025 The Kubernetes Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package git

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func TestClientFactorySigningKey(t *testing.T) {
	t.Parallel()

	// Generate an SSH signing key.
	keyDir := t.TempDir()
	keyPath := filepath.Join(keyDir, "id_ed25519")
	if out, err := exec.Command("ssh-keygen", "-t", "ed25519", "-f", keyPath, "-N", "").CombinedOutput(); err != nil {
		t.Fatalf("generating SSH key: %v\n%s", err, out)
	}

	// Create a source repo with a commit.
	repoBase := t.TempDir()
	repoDir := filepath.Join(repoBase, "org", "repo")
	if err := os.MkdirAll(repoDir, 0755); err != nil {
		t.Fatal(err)
	}
	runGit(t, repoDir, "init")
	runGit(t, repoDir, "config", "user.email", "test@test.test")
	runGit(t, repoDir, "config", "user.name", "test")
	runGit(t, repoDir, "config", "commit.gpgsign", "false")
	if err := os.WriteFile(filepath.Join(repoDir, "file.txt"), []byte("hello\n"), 0644); err != nil {
		t.Fatal(err)
	}
	runGit(t, repoDir, "add", "file.txt")
	runGit(t, repoDir, "commit", "-m", "initial")

	// Create a patch to apply.
	patchFile := filepath.Join(keyDir, "test.patch")
	patchContent := `From 0000000000000000000000000000000000000000 Mon Sep 17 00:00:00 2001
From: Test User <test@test.test>
Date: Thu, 01 Jan 2026 00:00:00 +0000
Subject: [PATCH] update file

---
 file.txt | 2 +-
 1 file changed, 1 insertion(+), 1 deletion(-)

diff --git a/file.txt b/file.txt
index ce01362..94954ab 100644
--- a/file.txt
+++ b/file.txt
@@ -1 +1 @@
-hello
+world
--
2.40.0
`
	if err := os.WriteFile(patchFile, []byte(patchContent), 0644); err != nil {
		t.Fatal(err)
	}

	// Create factory with signing key.
	factory, err := NewLocalClientFactory(repoBase,
		func() (string, string, error) { return "test", "test@test.test", nil },
		func(in []byte) []byte { return in },
		WithSigningKeyPath(keyPath),
	)
	if err != nil {
		t.Fatalf("creating factory: %v", err)
	}
	defer factory.Clean()

	client, err := factory.ClientFor("org", "repo")
	if err != nil {
		t.Fatalf("getting client: %v", err)
	}
	defer client.Clean()

	if err := client.Config("user.name", "test"); err != nil {
		t.Fatalf("setting user.name: %v", err)
	}
	if err := client.Config("user.email", "test@test.test"); err != nil {
		t.Fatalf("setting user.email: %v", err)
	}

	// Apply patch — should produce a signed commit.
	if err := client.Am(patchFile); err != nil {
		t.Fatalf("git am: %v", err)
	}

	// Verify the commit is signed.
	out, err := exec.Command("git", "-C", client.Directory(), "cat-file", "-p", "HEAD").CombinedOutput()
	if err != nil {
		t.Fatalf("inspecting commit: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "BEGIN SSH SIGNATURE") {
		t.Errorf("expected commit to contain SSH signature, got:\n%s", out)
	}
}
