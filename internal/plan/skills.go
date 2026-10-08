package plan

import (
	"bufio"
	"cmp"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/types/known/timestamppb"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/gen/go/plan/v1/planv1connect"
	"github.com/empowill/djinn/internal/store"
)

// SkillFolders are where a project keeps its skills, in this order: .agents/skills, which every agent reads, then
// .claude/skills. A name found in the first hides the same name in the second.
var SkillFolders = []string{filepath.Join(".agents", "skills"), filepath.Join(".claude", "skills")}

// SkillFile is the file that makes a folder a skill.
const SkillFile = "SKILL.md"

// SkillDir is a skill found on this machine.
type SkillDir struct {
	// Name is its folder's name.
	Name string
	// Dir is its folder, absolute.
	Dir string
	// Description is what it does, from its SKILL.md.
	Description string
}

// ProjectSkills are the skills held in the project folder dir, by name; none when dir is empty or has none.
func ProjectSkills(dir string) []SkillDir {
	if dir == "" {
		return nil
	}
	var out []SkillDir
	for _, folder := range SkillFolders {
		entries, err := os.ReadDir(filepath.Join(dir, folder))
		if err != nil {
			continue
		}
		for _, e := range entries {
			skill := filepath.Join(dir, folder, e.Name())
			if !isSkill(skill) || slices.ContainsFunc(out, func(s SkillDir) bool { return strings.EqualFold(s.Name, e.Name()) }) {
				continue
			}
			out = append(out, SkillDir{Name: e.Name(), Dir: skill, Description: Description(skill)})
		}
	}
	slices.SortFunc(out, func(a, b SkillDir) int { return cmp.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name)) })
	return out
}

// FindSkill is the skill name of the project folder dir, case ignored.
func FindSkill(dir, name string) (SkillDir, error) {
	if dir == "" {
		return SkillDir{}, errors.New("the project has no folder on this machine")
	}
	for _, s := range ProjectSkills(dir) {
		if strings.EqualFold(s.Name, name) {
			return s, nil
		}
	}
	return SkillDir{}, fmt.Errorf("no skill %s in %s nor %s of %s", name,
		filepath.ToSlash(SkillFolders[0]), filepath.ToSlash(SkillFolders[1]), dir)
}

// isSkill tells whether dir is a folder, or a link to one, holding a SKILL.md.
func isSkill(dir string) bool {
	info, err := os.Stat(filepath.Join(dir, SkillFile))
	return err == nil && info.Mode().IsRegular()
}

// Description is the description of the skill in dir, from the front matter of its SKILL.md: a line, or a folded
// block of indented lines; empty without one.
func Description(dir string) string {
	f, err := os.Open(filepath.Join(dir, SkillFile))
	if err != nil {
		return ""
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	if !sc.Scan() || strings.TrimSpace(sc.Text()) != "---" {
		return ""
	}
	var parts []string
	in := false
	for sc.Scan() {
		line := sc.Text()
		if strings.TrimSpace(line) == "---" {
			break
		}
		if in {
			if line != "" && line[0] != ' ' && line[0] != '\t' {
				break
			}
			parts = append(parts, strings.TrimSpace(line))
			continue
		}
		value, ok := strings.CutPrefix(line, "description:")
		if !ok {
			continue
		}
		value = strings.TrimSpace(value)
		if value == "" || strings.Trim(value, ">|-+") == "" {
			in = true // A block: the indented lines that follow.
			continue
		}
		return unquote(value)
	}
	return strings.TrimSpace(strings.Join(parts, " "))
}

func unquote(s string) string {
	if len(s) >= 2 && (s[0] == '"' || s[0] == '\'') && s[len(s)-1] == s[0] {
		return s[1 : len(s)-1]
	}
	return s
}

// Summoned is a skill a project summons, resolved on this machine: Missing says why it is not found.
type Summoned struct {
	SkillDir
	// Source is <project>/<skill>.
	Source string
	// Missing says why the skill cannot be found; empty when it is.
	Missing string
}

// SummonedSkills are the skills project summons, each found in its source project or said missing.
func SummonedSkills(ctx context.Context, r store.Reader, project *planv1.Project) ([]Summoned, error) {
	var out []Summoned
	for _, s := range project.GetSummons() {
		res := Summoned{Source: s.GetProjectId() + "/" + s.GetSkill(), SkillDir: SkillDir{Name: s.GetSkill()}}
		src, err := store.Get[*planv1.Project](ctx, r, s.GetProjectId())
		switch {
		case errors.Is(err, store.ErrNotFound):
			res.Missing = "its project is no longer known"
		case err != nil:
			return nil, err
		default:
			res.Source = src.GetName() + "/" + s.GetSkill()
			found, err := FindSkill(src.GetDirectory(), s.GetSkill())
			if err != nil {
				res.Missing = err.Error()
			} else {
				res.SkillDir = found
			}
		}
		out = append(out, res)
	}
	return out, nil
}

// Skills implements SkillService.
type Skills struct {
	planv1connect.UnimplementedSkillServiceHandler
	Store *store.Store
}

func (k *Skills) Summon(
	ctx context.Context, req *connect.Request[planv1.SkillServiceSummonRequest],
) (*connect.Response[planv1.SkillServiceSummonResponse], error) {
	srcName, name, _ := strings.Cut(req.Msg.GetSkill(), "/")
	var skill *planv1.Skill
	err := write(ctx, k.Store, req.Spec(), req.Msg, func(tx *store.Tx) error {
		into, err := projectNamed(ctx, tx, req.Msg.GetInto())
		if err != nil {
			return err
		}
		src, err := projectNamed(ctx, tx, srcName)
		if err != nil {
			return err
		}
		if src.GetId() == into.GetId() {
			return connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("%s holds the skill %s already: nothing to summon", into.GetName(), name))
		}
		found, err := FindSkill(src.GetDirectory(), name)
		if err != nil {
			return connect.NewError(connect.CodeNotFound, fmt.Errorf("project %s: %w", src.GetName(), err))
		}
		if _, err := FindSkill(into.GetDirectory(), found.Name); err == nil {
			return connect.NewError(connect.CodeAlreadyExists, fmt.Errorf("%s has a skill %s of its own", into.GetName(), found.Name))
		}
		for _, s := range into.GetSummons() {
			if !strings.EqualFold(s.GetSkill(), found.Name) {
				continue
			}
			if s.GetProjectId() == src.GetId() {
				skill = summonedSkill(into, Summoned{SkillDir: found, Source: src.GetName() + "/" + found.Name})
				return nil // Already summoned: nothing changes.
			}
			return connect.NewError(connect.CodeAlreadyExists, fmt.Errorf(
				"%s summons a skill %s from another project already: unsummon it first", into.GetName(), found.Name))
		}
		into.Summons = append(into.Summons, &planv1.Summon{ProjectId: src.GetId(), Skill: found.Name, CreateTime: timestamppb.Now()})
		skill = summonedSkill(into, Summoned{SkillDir: found, Source: src.GetName() + "/" + found.Name})
		return tx.Put(into)
	})
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&planv1.SkillServiceSummonResponse{Skill: skill}), nil
}

func (k *Skills) List(
	ctx context.Context, req *connect.Request[planv1.SkillServiceListRequest],
) (*connect.Response[planv1.SkillServiceListResponse], error) {
	var projects []*planv1.Project
	if name := req.Msg.GetProject(); name != "" {
		p, err := projectNamed(ctx, k.Store, name)
		if err != nil {
			return nil, Status(err)
		}
		projects = []*planv1.Project{p}
	} else {
		var err error
		if projects, err = store.List[*planv1.Project](ctx, k.Store, nil); err != nil {
			return nil, Status(err)
		}
		slices.SortFunc(projects, func(a, b *planv1.Project) int {
			return cmp.Compare(strings.ToLower(a.GetName()), strings.ToLower(b.GetName()))
		})
	}
	res := &planv1.SkillServiceListResponse{}
	for _, p := range projects {
		for _, s := range ProjectSkills(p.GetDirectory()) {
			res.Skills = append(res.Skills, &planv1.Skill{
				Project: p.GetName(), Name: s.Name, Directory: s.Dir, Description: s.Description,
			})
		}
		summoned, err := SummonedSkills(ctx, k.Store, p)
		if err != nil {
			return nil, Status(err)
		}
		for _, s := range summoned {
			res.Skills = append(res.Skills, summonedSkill(p, s))
		}
	}
	return connect.NewResponse(res), nil
}

func (k *Skills) Unsummon(
	ctx context.Context, req *connect.Request[planv1.SkillServiceUnsummonRequest],
) (*connect.Response[planv1.SkillServiceUnsummonResponse], error) {
	srcName, name, _ := strings.Cut(req.Msg.GetSkill(), "/")
	var from *planv1.Project
	err := write(ctx, k.Store, req.Spec(), req.Msg, func(tx *store.Tx) error {
		var err error
		if from, err = projectNamed(ctx, tx, req.Msg.GetFrom()); err != nil {
			return err
		}
		// The source is matched by name, or by identifier when its project is no longer known.
		src, err := projectNamed(ctx, tx, srcName)
		if err != nil && !errors.Is(err, store.ErrNotFound) {
			return err
		}
		n := len(from.GetSummons())
		from.Summons = slices.DeleteFunc(from.Summons, func(s *planv1.Summon) bool {
			sameSource := strings.EqualFold(s.GetProjectId(), srcName) || (src != nil && s.GetProjectId() == src.GetId())
			return sameSource && strings.EqualFold(s.GetSkill(), name)
		})
		if len(from.GetSummons()) == n {
			return connect.NewError(connect.CodeNotFound, fmt.Errorf("%s does not summon %s", from.GetName(), req.Msg.GetSkill()))
		}
		return tx.Put(from)
	})
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&planv1.SkillServiceUnsummonResponse{Project: from}), nil
}

// summonedSkill is the summoned skill s as project p sees it.
func summonedSkill(p *planv1.Project, s Summoned) *planv1.Skill {
	return &planv1.Skill{
		Project: p.GetName(), Name: s.Name, Source: s.Source, Directory: s.Dir, Description: s.Description, Missing: s.Missing,
	}
}

// projectNamed is the project of this name, case ignored, or of this identifier.
func projectNamed(ctx context.Context, r store.Reader, name string) (*planv1.Project, error) {
	all, err := store.List[*planv1.Project](ctx, r, nil)
	if err != nil {
		return nil, err
	}
	for _, p := range all {
		if strings.EqualFold(p.GetName(), name) || strings.EqualFold(p.GetId(), name) {
			return p, nil
		}
	}
	return nil, connect.NewError(connect.CodeNotFound, fmt.Errorf("no project %s: djinn project list shows them, %w", name, store.ErrNotFound))
}
