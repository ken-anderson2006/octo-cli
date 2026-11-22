package cmd

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/spf13/cobra"

	"github.com/Mininglamp-OSS/octo-cli/internal/cmdutil"
	"github.com/Mininglamp-OSS/octo-cli/internal/config"
	"github.com/Mininglamp-OSS/octo-cli/internal/output"
	"github.com/Mininglamp-OSS/octo-cli/skills"
)

// newSkillsCmd returns `octo-cli skills`. Three mutually exclusive modes:
//   - `octo-cli skills`: list embedded skills (name + description).
//   - `octo-cli skills <name>`: print one skill's name, description, content
//     (SKILL.md), and any progressive-disclosure reference files.
//   - `octo-cli skills --install <dir>`: write every skill to <dir>/<name>/SKILL.md.
func newSkillsCmd(f *cmdutil.Factory) *cobra.Command {
	var install string

	cmd := &cobra.Command{
		Use:   "skills [name]",
		Short: "List, print, or install the embedded agent skill docs",
		Long: `Agent-facing SKILL.md docs are embedded in this binary.

  octo-cli skills                    list available skills
  octo-cli skills <name>             print one skill (name, description, content + references)
  octo-cli skills --install <dir>    write every skill (SKILL.md + references) to <dir>/<name>/

Installation changes local files and rejects --dry-run; listing and showing remain available.`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if cmd.Flags().Changed("install") && f.Globals.DryRun {
				ee := output.ErrValidation(
					"--dry-run is not supported with skills --install",
					"omit --dry-run to write skills to the target directory",
				)
				_ = f.EmitError(ee) //nolint:errcheck // best-effort emit before returning err
				return ee
			}
			if cmd.Flags().Changed("install") && install == "" {
				ee := output.ErrValidation(
					"--install requires a directory",
					"pass a target like --install ~/.config/octo/skills",
				)
				_ = f.EmitError(ee) //nolint:errcheck // best-effort emit before returning err
				return ee
			}
			var files fs.FS = skills.Framework()
			if os.Getenv(config.EnvCLIAPIContract) == config.CLIAPIContractBot && cmd.Root().Annotations["frameworkMCP"] != "true" {
				files = skills.FS
			}
			entries, err := skills.List(files)
			if err != nil {
				_ = f.EmitError(err) //nolint:errcheck // best-effort emit before returning err
				return err
			}
			switch {
			case install != "":
				if len(args) == 1 {
					ee := output.ErrValidation(
						"cannot combine a skill name with --install",
						"drop the name to install all skills, or drop --install to print one",
					)
					_ = f.EmitError(ee) //nolint:errcheck // best-effort emit before returning err
					return ee
				}
				return runSkillsInstall(f, entries, install)
			case len(args) == 1:
				return runSkillsShow(f, entries, args[0])
			default:
				return runSkillsList(f, entries)
			}
		},
	}

	cmd.Flags().StringVar(&install, "install", "", "write all skills to this directory (<dir>/<name>/SKILL.md)")
	return cmd
}

// runSkillsList emits every skill as {name, description}.
func runSkillsList(f *cmdutil.Factory, entries []skills.Entry) error {
	buf, err := json.Marshal(map[string]any{"skills": entries})
	if err != nil {
		_ = f.EmitError(err) //nolint:errcheck // best-effort emit before returning err
		return err
	}
	return f.EmitSuccess(buf)
}

// runSkillsShow emits one skill's full content — SKILL.md plus any
// progressive-disclosure reference files — or a validation error naming the
// available skills when name doesn't match. `content` holds SKILL.md;
// `references` maps each sibling reference filename to its content so the whole
// skill set is reprinted, not just the router.
func runSkillsShow(f *cmdutil.Factory, entries []skills.Entry, name string) error {
	for _, s := range entries {
		if s.Name != name {
			continue
		}
		detail, rerr := s.Load()
		if rerr != nil {
			_ = f.EmitError(rerr) //nolint:errcheck // best-effort emit before returning err
			return rerr
		}
		payload := map[string]any{
			"name":        s.Name,
			"description": s.Description,
			"content":     detail.Content,
		}
		if len(detail.References) > 0 {
			payload["references"] = detail.References
		}
		buf, err := json.Marshal(payload)
		if err != nil {
			_ = f.EmitError(err) //nolint:errcheck // best-effort emit before returning err
			return err
		}
		return f.EmitSuccess(buf)
	}
	names := make([]string, len(entries))
	for i, s := range entries {
		names[i] = s.Name
	}
	ee := output.ErrValidation(
		fmt.Sprintf("unknown skill %q", name),
		fmt.Sprintf("available: %s", strings.Join(names, ", ")),
	)
	_ = f.EmitError(ee) //nolint:errcheck // best-effort emit before returning err
	return ee
}

// runSkillsInstall writes every enabled skill's whole markdown set to
// <dir>/<name>/ — SKILL.md plus any sibling progressive-disclosure reference
// files and reports the files written.
func runSkillsInstall(f *cmdutil.Factory, entries []skills.Entry, dir string) error {
	written := make([]string, 0, len(entries))
	for _, entry := range entries {
		skill, err := entry.Load()
		if err != nil {
			_ = f.EmitError(err) //nolint:errcheck // best-effort emit before returning err
			return err
		}
		skillDir := filepath.Join(dir, entry.Name)
		if err := os.MkdirAll(skillDir, 0o755); err != nil {
			_ = f.EmitError(err) //nolint:errcheck // best-effort emit before returning err
			return err
		}
		files := skill.References
		files["SKILL.md"] = skill.Content
		for _, name := range slices.Sorted(maps.Keys(files)) {
			dst := filepath.Join(skillDir, name)
			if err := os.WriteFile(dst, []byte(files[name]), 0o644); err != nil {
				_ = f.EmitError(err) //nolint:errcheck // best-effort emit before returning err
				return err
			}
			written = append(written, dst)
		}
	}
	buf, err := json.Marshal(map[string]any{"dir": dir, "installed": written})
	if err != nil {
		_ = f.EmitError(err) //nolint:errcheck // best-effort emit before returning err
		return err
	}
	return f.EmitSuccess(buf)
}
