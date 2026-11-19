package cmd

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/Mininglamp-OSS/octo-cli/internal/config"
	"github.com/Mininglamp-OSS/octo-cli/skills"
)

// A config without a token must still let `octo-cli skills` run — the command is
// offline and on the skipValidation list.
func TestCmd_SkillsList(t *testing.T) {
	f := newTestFactoryWithReg()
	f.SetConfig(&config.Config{Format: "json"})

	out, _, err := execRoot(t, f, "skills")
	if err != nil {
		t.Fatalf("skills: %v", err)
	}
	var env map[string]any
	if jerr := json.Unmarshal([]byte(out), &env); jerr != nil {
		t.Fatalf("unmarshal: %v\n%s", jerr, out)
	}
	if env["ok"] != true {
		t.Errorf("ok = %v", env["ok"])
	}
	data, _ := env["data"].(map[string]any)
	list, _ := data["skills"].([]any)
	if len(list) == 0 {
		t.Errorf("expected a non-empty skills list, got %v", data)
	}
}

func TestCmd_SkillsShow(t *testing.T) {
	f := newTestFactoryWithReg()
	f.SetConfig(&config.Config{Format: "json"})

	out, _, err := execRoot(t, f, "skills", "octo-shared")
	if err != nil {
		t.Fatalf("skills octo-shared: %v", err)
	}
	var env map[string]any
	if jerr := json.Unmarshal([]byte(out), &env); jerr != nil {
		t.Fatalf("unmarshal: %v\n%s", jerr, out)
	}
	data, _ := env["data"].(map[string]any)
	if data["name"] != "octo-shared" {
		t.Errorf("name = %v", data["name"])
	}
	if content, _ := data["content"].(string); !strings.Contains(content, "octo-shared") {
		t.Errorf("content missing skill body: %.80q", content)
	}
}

func TestCmd_SkillsUnknown(t *testing.T) {
	f := newTestFactoryWithReg()
	f.SetConfig(&config.Config{Format: "json"})

	_, errOut, err := execRoot(t, f, "skills", "no-such-skill")
	if err == nil {
		t.Fatal("expected error for unknown skill")
	}
	if !strings.Contains(errOut, "validation") {
		t.Errorf("error envelope missing validation type: %s", errOut)
	}
	if !strings.Contains(errOut, "no-such-skill") {
		t.Errorf("error should name the unknown skill: %s", errOut)
	}
}

func TestCmd_SkillsInstall(t *testing.T) {
	dir := t.TempDir()
	f := newTestFactoryWithReg()
	f.SetConfig(&config.Config{Format: "json"})

	out, _, err := execRoot(t, f, "skills", "--install", dir)
	if err != nil {
		t.Fatalf("skills --install: %v", err)
	}
	var env map[string]any
	if jerr := json.Unmarshal([]byte(out), &env); jerr != nil {
		t.Fatalf("unmarshal: %v\n%s", jerr, out)
	}
	data, _ := env["data"].(map[string]any)
	installed, _ := data["installed"].([]any)
	if len(installed) == 0 {
		t.Fatalf("nothing installed: %v", data)
	}
	for _, p := range installed {
		ps, _ := p.(string)
		fi, statErr := os.Stat(ps)
		if statErr != nil || fi.Size() == 0 {
			t.Errorf("installed file missing/empty: %s (%v)", ps, statErr)
		}
	}
	// Verify the expected on-disk layout.
	if _, statErr := os.Stat(filepath.Join(dir, "octo-shared", "SKILL.md")); statErr != nil {
		t.Errorf("expected octo-shared/SKILL.md under install dir: %v", statErr)
	}

}

func TestCmd_SkillsInstallEmptyDir(t *testing.T) {
	f := newTestFactoryWithReg()
	f.SetConfig(&config.Config{Format: "json"})

	_, errOut, err := execRoot(t, f, "skills", "--install", "")
	if err == nil {
		t.Fatal("expected error when --install is given an empty directory")
	}
	if !strings.Contains(errOut, "validation") {
		t.Errorf("expected a validation error, got: %s", errOut)
	}
}

func TestCmd_SkillsInstallRejectsName(t *testing.T) {
	dir := t.TempDir()
	f := newTestFactoryWithReg()
	f.SetConfig(&config.Config{Format: "json"})

	_, errOut, err := execRoot(t, f, "skills", "octo-shared", "--install", dir)
	if err == nil {
		t.Fatal("expected error when combining a skill name with --install")
	}
	if !strings.Contains(errOut, "validation") {
		t.Errorf("expected a validation error, got: %s", errOut)
	}
}

func TestSkillsInstallFromIndex(t *testing.T) {
	files := fstest.MapFS{
		"example/SKILL.md": {Data: []byte("---\ndescription: fixture\n---\n")},
		"example/A.md":     {Data: []byte("before\r\n")},
		"example/z.md":     {Data: []byte("after\n")},
	}
	entries, err := skills.List(files)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	f := newTestFactoryWithReg()
	if err := runSkillsInstall(f.Factory, entries, dir); err != nil {
		t.Fatal(err)
	}
	var env struct {
		Data struct{ Installed []string }
	}
	if err := json.Unmarshal(f.Out.Bytes(), &env); err != nil {
		t.Fatal(err)
	}
	var paths []string
	for _, name := range []string{"example/A.md", "example/SKILL.md", "example/z.md"} {
		dst := filepath.Join(dir, filepath.FromSlash(name))
		paths = append(paths, dst)
		b, err := os.ReadFile(dst)
		if err != nil || string(b) != string(files[name].Data) {
			t.Errorf("installed %s = %q, %v; want %q", dst, b, err, files[name].Data)
		}
	}
	if !slices.Equal(env.Data.Installed, paths) {
		t.Errorf("installed list = %v, want %v", env.Data.Installed, paths)
	}
}
