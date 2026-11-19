package skills

import (
	"fmt"
	"io/fs"
	"path"
	"strings"
	"unicode/utf8"
)

// Entry is the lightweight CLI index record. Only its name and description
// appear in the index; the Markdown and reference names support later reads.
type Entry struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	fsys        fs.FS
	content     string
	references  []string
}

// Skill contains the complete published text of an enabled skill.
type Skill struct {
	Name        string            `json:"name"`
	Description string            `json:"description"`
	Content     string            `json:"content"`
	References  map[string]string `json:"references"`
}

// Result is the full set of bundled, enabled skills.
type Result struct {
	Skills []Skill `json:"skills"`
}

// List finds enabled SKILL.md files in directory-name order. Reference contents
// are read on demand, so an index or single-skill query only reads what it needs.
func List(fsys fs.FS) ([]Entry, error) {
	dirs, err := fs.ReadDir(fsys, ".")
	if err != nil {
		return nil, fmt.Errorf("list skills: %w", err)
	}
	entries := make([]Entry, 0, len(dirs))
	for _, dir := range dirs {
		if !dir.IsDir() {
			continue
		}
		name := dir.Name()
		files, err := fs.ReadDir(fsys, name)
		if err != nil {
			return nil, fmt.Errorf("list skill %q: %w", name, err)
		}
		var hasMain bool
		var references []string
		for _, file := range files {
			if file.IsDir() {
				continue
			}
			switch {
			case file.Name() == "SKILL.md":
				hasMain = true
			case strings.HasSuffix(file.Name(), ".md"):
				references = append(references, file.Name())
			}
		}
		if !hasMain {
			continue
		}
		content, err := readText(fsys, path.Join(name, "SKILL.md"))
		if err != nil {
			return nil, err
		}
		if frontmatterValue(content, "disabled") == "true" {
			continue
		}
		entries = append(entries, Entry{
			Name:        name,
			Description: frontmatterValue(content, "description"),
			fsys:        fsys,
			content:     content,
			references:  references,
		})
	}
	return entries, nil
}

// Load reads the entry's sibling Markdown references from its source filesystem.
func (e Entry) Load() (Skill, error) {
	refs := make(map[string]string, len(e.references))
	for _, name := range e.references {
		content, err := readText(e.fsys, path.Join(e.Name, name))
		if err != nil {
			return Skill{}, err
		}
		refs[name] = content
	}
	return Skill{Name: e.Name, Description: e.Description, Content: e.content, References: refs}, nil
}

// LoadAll reads every enabled skill and its references, returning a single
// complete result or an error if any required directory or file can't be read.
func LoadAll(fsys fs.FS) (Result, error) {
	entries, err := List(fsys)
	if err != nil {
		return Result{}, err
	}
	all := make([]Skill, 0, len(entries))
	for _, entry := range entries {
		skill, err := entry.Load()
		if err != nil {
			return Result{}, err
		}
		// JSON replaces malformed UTF-8. Reject it so published content
		// always round-trips to the original Markdown bytes.
		if !utf8.ValidString(skill.Content) {
			return Result{}, fmt.Errorf("%s/SKILL.md contains invalid UTF-8", skill.Name)
		}
		for filename, content := range skill.References {
			if !utf8.ValidString(content) {
				return Result{}, fmt.Errorf("%s/%s contains invalid UTF-8", skill.Name, filename)
			}
		}
		all = append(all, skill)
	}
	return Result{Skills: all}, nil
}

func readText(fsys fs.FS, name string) (string, error) {
	b, err := fs.ReadFile(fsys, name)
	if err != nil {
		return "", fmt.Errorf("read %s: %w", name, err)
	}
	return string(b), nil
}

// The embedded skills use single-line frontmatter values.
func frontmatterValue(content, key string) string {
	inFrontmatter := false
	for line := range strings.SplitSeq(content, "\n") {
		line = strings.TrimSpace(line)
		if line == "---" {
			if inFrontmatter {
				break
			}
			inFrontmatter = true
			continue
		}
		if inFrontmatter {
			if value, found := strings.CutPrefix(line, key+":"); found {
				return strings.TrimSpace(value)
			}
		}
	}
	return ""
}
