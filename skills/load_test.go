package skills

import (
	"encoding/json"
	"errors"
	"io/fs"
	"reflect"
	"testing"
	"testing/fstest"
)

type faultyFS struct {
	fstest.MapFS
	fail string
}

func (f faultyFS) ReadDir(name string) ([]fs.DirEntry, error) {
	if name == f.fail {
		return nil, fs.ErrPermission
	}
	return f.MapFS.ReadDir(name)
}

func (f faultyFS) ReadFile(name string) ([]byte, error) {
	if name == f.fail {
		return nil, fs.ErrPermission
	}
	return f.MapFS.ReadFile(name)
}

func TestLoadAll(t *testing.T) {
	const main = "---\r\nname: an-alias\r\ndescription:  Two words  \r\n---\r\n\r\nHello 世界!\r\n"
	const ref = "\n## Details\n\n原文\n"
	files := fstest.MapFS{
		"z/SKILL.md":       {Data: []byte("# Z\n")},
		"a/SKILL.md":       {Data: []byte(main)},
		"a/details.md":     {Data: []byte(ref)},
		"a/icons.svg":      {},
		"a/nested.md/x.md": {},
		"orphan/note.md":   {},
		"off/SKILL.md":     {Data: []byte("---\ndisabled: true\n---\n")},
		"off/private.md":   {},
		"README.md":        {},
	}
	// A disabled skill's references must never be opened.
	got, err := LoadAll(faultyFS{files, "off/private.md"})
	if err != nil {
		t.Fatal(err)
	}
	want := Result{Skills: []Skill{
		{Name: "a", Description: "Two words", Content: main, References: map[string]string{"details.md": ref}},
		{Name: "z", Content: "# Z\n", References: map[string]string{}},
	}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("LoadAll = %#v, want %#v", got, want)
	}
}

func TestLoadAllEmpty(t *testing.T) {
	got, err := LoadAll(fstest.MapFS{})
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(got)
	if err != nil || string(raw) != `{"skills":[]}` {
		t.Fatalf("empty result = %s, %v", raw, err)
	}
}

func TestLoadAllReadErrors(t *testing.T) {
	files := fstest.MapFS{
		"a/SKILL.md": {},
		"b/SKILL.md": {},
		"b/ref.md":   {},
	}
	for _, fail := range []string{".", "b", "b/SKILL.md", "b/ref.md"} {
		t.Run(fail, func(t *testing.T) {
			got, err := LoadAll(faultyFS{files, fail})
			if !errors.Is(err, fs.ErrPermission) || len(got.Skills) != 0 {
				t.Fatalf("LoadAll returned %d skills, %v; want no skills and a read error", len(got.Skills), err)
			}
		})
	}
}

func TestParseSkillDescription(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"basic", "---\nname: x\ndescription: hello world\n---\nbody", "hello world"},
		{"trims surrounding space", "---\ndescription:   spaced   \n---", "spaced"},
		{"no frontmatter", "# title\ndescription: ignored", ""},
		{"description only in body is ignored", "---\nname: x\n---\ndescription: in body", ""},
		{"empty input", "", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := frontmatterValue(c.in, "description"); got != c.want {
				t.Errorf("description of %q = %q, want %q", c.in, got, c.want)
			}
		})
	}
}

func TestLoadSkills(t *testing.T) {
	entries, err := List(FS)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(entries) == 0 {
		t.Fatal("expected at least one embedded skill")
	}
	for i, s := range entries {
		if s.Name == "" || s.Description == "" || len(s.content) == 0 {
			t.Errorf("entry %d incomplete: name=%q desc=%q contentLen=%d", i, s.Name, s.Description, len(s.content))
		}
		if i > 0 && entries[i-1].Name >= s.Name {
			t.Errorf("entries not sorted ascending: %q before %q", entries[i-1].Name, s.Name)
		}
	}
	// octo-shared is the foundational skill; it must always be embedded.
	if !hasSkill(entries, "octo-shared") {
		names := make([]string, len(entries))
		for i, s := range entries {
			names[i] = s.Name
		}
		t.Errorf("octo-shared not found among %v", names)
	}
}

func hasSkill(entries []Entry, name string) bool {
	for _, s := range entries {
		if s.Name == name {
			return true
		}
	}
	return false
}

func TestLoadAllInvalidUTF8(t *testing.T) {
	for _, name := range []string{"z/SKILL.md", "z/ref.md"} {
		t.Run(name, func(t *testing.T) {
			files := fstest.MapFS{
				"a/SKILL.md": {},
				"z/SKILL.md": {},
				"z/ref.md":   {},
			}
			files[name].Data = []byte{0xff}
			got, err := LoadAll(files)
			if err == nil || len(got.Skills) != 0 {
				t.Fatalf("invalid UTF-8 in %s: got %d skills, %v; want no skills and an error", name, len(got.Skills), err)
			}
		})
	}
}
