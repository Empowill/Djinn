package main

import (
	"context"
	"database/sql"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"google.golang.org/protobuf/proto"
	_ "modernc.org/sqlite"
)

var reMermaid = regexp.MustCompile("(?s)```mermaid\n.*?\n```")

const (
	diaRouteSVG = `<svg class="dia" id="dia-route" viewBox="0 0 740 250" width="100%" style="min-width:640px" role="img" aria-label="Route and RouteOption">
<defs>
<marker id="dia-route-d" viewBox="0 0 12 12" refX="1" refY="6" markerWidth="10" markerHeight="10" orient="auto"><path d="M1 6L6 2L11 6L6 10z" fill="var(--raise)" stroke="var(--ink-faint)" stroke-width="1.2"/></marker>
</defs>
<path d="M290 100 L350 100" fill="none" stroke-width="1.2" stroke="var(--line-strong)" marker-start="url(#dia-route-d)"/>
<g class="d-question">
<rect x="20" y="20" width="270" height="120" rx="9" fill="var(--w)" stroke="var(--c)" stroke-width="1.4"/>
<text x="31" y="40" class="bt">Route</text>
<text x="279" y="40" class="tg" text-anchor="end">in Question, InboxItem</text>
<line x1="20" x2="290" y1="50" y2="50" stroke="var(--c)" stroke-opacity="0.45"/>
<text x="31" y="68" class="fn">request<tspan class="ft"> string</tspan></text>
<text x="31" y="84" class="fn">from_wish_id<tspan class="ft"> string</tspan></text>
<text x="279" y="84" class="ff" text-anchor="end">→ Wish</text>
<text x="31" y="100" class="fn">options[]<tspan class="ft"> RouteOption</tspan></text>
<text x="279" y="100" class="ff" text-anchor="end">◆</text>
</g>
<g class="d-question">
<rect x="350" y="20" width="370" height="200" rx="9" fill="var(--w)" stroke="var(--c)" stroke-width="1.4"/>
<text x="361" y="40" class="bt">RouteOption</text>
<line x1="350" x2="720" y1="50" y2="50" stroke="var(--c)" stroke-opacity="0.45"/>
<text x="361" y="68" class="fn">kind<tspan class="ft"> RouteKind</tspan></text>
<text x="361" y="84" class="fn">wish_id<tspan class="ft"> string</tspan></text>
<text x="709" y="84" class="ff" text-anchor="end">→ Wish</text>
<text x="361" y="100" class="fn">title<tspan class="ft"> string</tspan></text>
<text x="361" y="116" class="fn">project_ids[]<tspan class="ft"> string</tspan></text>
<text x="709" y="116" class="ff" text-anchor="end">→ Project</text>
<text x="361" y="132" class="fn">pause_wish_id<tspan class="ft"> string</tspan></text>
<text x="709" y="132" class="ff" text-anchor="end">→ Wish</text>
<text x="361" y="148" class="fn">score<tspan class="ft"> int32</tspan></text>
<text x="361" y="164" class="fn">reason<tspan class="ft"> string</tspan></text>
<text x="361" y="180" class="fn">template<tspan class="ft"> WishTemplate</tspan></text>
</g>
</svg>`

	diaSkillSVG = `<svg class="dia" id="dia-skill" viewBox="0 0 940 260" width="100%" style="min-width:800px" role="img" aria-label="Skill, WishTemplate and InboxItem">
<defs>
<marker id="dia-skill-a" viewBox="0 0 10 10" refX="9" refY="5" markerWidth="7" markerHeight="7" orient="auto-start-reverse"><path d="M0 0L10 5L0 10z" fill="var(--ink-faint)"/></marker>
</defs>
<path d="M280 80 L330 80" fill="none" stroke-width="1.2" stroke="var(--ink-faint)" stroke-dasharray="4 3" marker-end="url(#dia-skill-a)"/>
<text x="305" y="74" class="tg" text-anchor="middle">wish</text>
<path d="M280 180 L630 180" fill="none" stroke-width="1.2" stroke="var(--ink-faint)" stroke-dasharray="4 3" marker-end="url(#dia-skill-a)"/>
<text x="475" y="174" class="tg" text-anchor="middle">source</text>
<g class="d-projects">
<rect x="20" y="20" width="260" height="224" rx="9" fill="var(--raise)" stroke="var(--c)" stroke-width="1.4"/>
<text x="31" y="40" class="bt">Skill</text>
<text x="269" y="40" class="tg" text-anchor="end">computed from SKILL.md</text>
<line x1="20" x2="280" y1="50" y2="50" stroke="var(--c)" stroke-opacity="0.45"/>
<text x="31" y="68" class="fn">project<tspan class="ft"> string</tspan></text>
<text x="31" y="84" class="fn">name<tspan class="ft"> string</tspan></text>
<text x="31" y="100" class="fn">source<tspan class="ft"> string</tspan></text>
<text x="31" y="116" class="fn">directory<tspan class="ft"> string</tspan></text>
<text x="31" y="132" class="fn">description<tspan class="ft"> string</tspan></text>
<text x="31" y="148" class="fn">missing<tspan class="ft"> string</tspan></text>
<text x="31" y="164" class="fn">template<tspan class="ft"> string</tspan></text>
<text x="31" y="180" class="fn">template_error<tspan class="ft"> string</tspan></text>
<text x="31" y="196" class="fn">inbox_source<tspan class="ft"> string</tspan></text>
<text x="31" y="212" class="fn">inbox_source_error<tspan class="ft"> string</tspan></text>
</g>
<g class="d-wish">
<rect x="330" y="20" width="260" height="136" rx="9" fill="var(--raise)" stroke="var(--c)" stroke-width="1"/>
<text x="341" y="40" class="bt">WishTemplate</text>
<text x="579" y="40" class="tg" text-anchor="end">copied into Wish</text>
<line x1="330" x2="590" y1="50" y2="50" stroke="var(--c)" stroke-opacity="0.45"/>
<text x="341" y="68" class="fn">skill<tspan class="ft"> string</tspan></text>
<text x="341" y="84" class="fn">project_id<tspan class="ft"> string</tspan></text>
<text x="579" y="84" class="ff" text-anchor="end">→ Project</text>
<text x="341" y="100" class="fn">watch<tspan class="ft"> string</tspan></text>
<text x="341" y="116" class="fn">done_when<tspan class="ft"> string</tspan></text>
<text x="341" y="132" class="fn">restart<tspan class="ft"> bool</tspan></text>
</g>
<g class="d-projects">
<rect x="630" y="20" width="290" height="224" rx="9" fill="var(--w)" stroke="var(--c)" stroke-width="1.4"/>
<text x="641" y="40" class="bt">InboxItem</text>
<text x="909" y="40" class="tg" text-anchor="end">table inbox_item</text>
<line x1="630" x2="920" y1="50" y2="50" stroke="var(--c)" stroke-opacity="0.45"/>
<text x="641" y="68" class="fn">id<tspan class="ft"> string</tspan></text>
<text x="909" y="68" class="ff" text-anchor="end">PK</text>
<text x="641" y="84" class="fn">source<tspan class="ft"> string</tspan></text>
<text x="641" y="100" class="fn">project_id<tspan class="ft"> string</tspan></text>
<text x="909" y="100" class="ff" text-anchor="end">idx → Project</text>
<text x="641" y="116" class="fn">key<tspan class="ft"> string</tspan></text>
<text x="909" y="116" class="ff" text-anchor="end">uq</text>
<text x="641" y="132" class="fn">text<tspan class="ft"> string</tspan></text>
<text x="641" y="148" class="fn">state<tspan class="ft"> InboxState</tspan></text>
<text x="641" y="164" class="fn">create_time<tspan class="ft"> Timestamp</tspan></text>
<text x="641" y="180" class="fn">route<tspan class="ft"> Route</tspan></text>
<text x="641" y="196" class="fn">wish_id<tspan class="ft"> string</tspan></text>
<text x="909" y="196" class="ff" text-anchor="end">idx → Wish</text>
<text x="641" y="212" class="fn">settle_time<tspan class="ft"> Timestamp</tspan></text>
</g>
</svg>`

	diaSettingsSVG = `<svg class="dia" id="dia-settings" viewBox="0 0 940 250" width="100%" style="min-width:800px" role="img" aria-label="Settings, Shortcut, Provider, Terminal and Manifest">
<defs>
<marker id="dia-settings-a" viewBox="0 0 10 10" refX="9" refY="5" markerWidth="7" markerHeight="7" orient="auto-start-reverse"><path d="M0 0L10 5L0 10z" fill="var(--ink-faint)"/></marker>
</defs>
<path d="M190 56 L240 56" fill="none" stroke-width="1.2" stroke="var(--ink-faint)" stroke-dasharray="4 3" marker-end="url(#dia-settings-a)"/>
<g class="d-projects">
<rect x="20" y="20" width="170" height="72" rx="9" fill="var(--raise)" stroke="var(--c)" stroke-width="1"/>
<text x="31" y="40" class="bt">Settings</text>
<text x="179" y="40" class="tg" text-anchor="end">settings.json</text>
<line x1="20" x2="190" y1="50" y2="50" stroke="var(--c)" stroke-opacity="0.45"/>
<text x="31" y="68" class="fn">shortcut<tspan class="ft"> string</tspan></text>
</g>
<g class="d-projects">
<rect x="240" y="20" width="200" height="120" rx="9" fill="var(--raise)" stroke="var(--c)" stroke-width="1"/>
<text x="251" y="40" class="bt">Shortcut</text>
<text x="429" y="40" class="tg" text-anchor="end">computed</text>
<line x1="240" x2="440" y1="50" y2="50" stroke="var(--c)" stroke-opacity="0.45"/>
<text x="251" y="68" class="fn">chord<tspan class="ft"> string</tspan></text>
<text x="251" y="84" class="fn">default_chord<tspan class="ft"> string</tspan></text>
<text x="251" y="100" class="fn">available<tspan class="ft"> bool</tspan></text>
<text x="251" y="116" class="fn">problem<tspan class="ft"> string</tspan></text>
</g>
<g class="d-projects">
<rect x="470" y="20" width="200" height="120" rx="9" fill="var(--raise)" stroke="var(--c)" stroke-width="1"/>
<text x="481" y="40" class="bt">Provider</text>
<text x="659" y="40" class="tg" text-anchor="end">computed, ui.v1</text>
<line x1="470" x2="670" y1="50" y2="50" stroke="var(--c)" stroke-opacity="0.45"/>
<text x="481" y="68" class="fn">id<tspan class="ft"> string</tspan></text>
<text x="481" y="84" class="fn">name<tspan class="ft"> string</tspan></text>
<text x="481" y="100" class="fn">available<tspan class="ft"> bool</tspan></text>
<text x="481" y="116" class="fn">command<tspan class="ft"> string</tspan></text>
</g>
<g class="d-machine">
<rect x="700" y="20" width="220" height="190" rx="9" fill="var(--raise)" stroke="var(--c)" stroke-width="1"/>
<text x="711" y="40" class="bt">Terminal</text>
<text x="909" y="40" class="tg" text-anchor="end">memory</text>
<line x1="700" x2="920" y1="50" y2="50" stroke="var(--c)" stroke-opacity="0.45"/>
<text x="711" y="68" class="fn">id<tspan class="ft"> string</tspan></text>
<text x="711" y="84" class="fn">name<tspan class="ft"> string</tspan></text>
<text x="711" y="100" class="fn">command[]<tspan class="ft"> string</tspan></text>
<text x="711" y="116" class="fn">directory<tspan class="ft"> string</tspan></text>
<text x="711" y="132" class="fn">cols · rows<tspan class="ft"> uint32</tspan></text>
<text x="711" y="148" class="fn">exited<tspan class="ft"> bool</tspan></text>
<text x="711" y="164" class="fn">exit_code<tspan class="ft"> int32</tspan></text>
</g>
<g class="d-block">
<rect x="20" y="120" width="190" height="104" rx="9" fill="var(--raise)" stroke="var(--c)" stroke-width="1"/>
<text x="31" y="140" class="bt">Manifest</text>
<text x="199" y="140" class="tg" text-anchor="end">in a backup</text>
<line x1="20" x2="210" y1="150" y2="150" stroke="var(--c)" stroke-opacity="0.45"/>
<text x="31" y="168" class="fn">format<tspan class="ft"> int32</tspan></text>
<text x="31" y="184" class="fn">version<tspan class="ft"> string</tspan></text>
<text x="31" y="200" class="fn">create_time<tspan class="ft"> Timestamp</tspan></text>
</g>
</svg>`

	diaExportSVG = `<svg class="dia" id="dia-export" viewBox="0 0 840 240" width="100%" style="min-width:720px" role="img" aria-label="WishExport, ProjectRef and Command">
<defs>
<marker id="dia-export-d" viewBox="0 0 12 12" refX="1" refY="6" markerWidth="10" markerHeight="10" orient="auto"><path d="M1 6L6 2L11 6L6 10z" fill="var(--raise)" stroke="var(--ink-faint)" stroke-width="1.2"/></marker>
</defs>
<path d="M280 116 L330 116" fill="none" stroke-width="1.2" stroke="var(--line-strong)" marker-start="url(#dia-export-d)"/>
<path d="M280 164 L580 164 L580 120 L600 120" fill="none" stroke-width="1.2" stroke="var(--line-strong)" marker-start="url(#dia-export-d)"/>
<g class="d-wish">
<rect x="20" y="20" width="260" height="190" rx="9" fill="var(--w)" stroke="var(--c)" stroke-width="1.4"/>
<text x="31" y="40" class="bt">WishExport</text>
<text x="269" y="40" class="tg" text-anchor="end">file .djinn</text>
<line x1="20" x2="280" y1="50" y2="50" stroke="var(--c)" stroke-opacity="0.45"/>
<text x="31" y="68" class="fn">version<tspan class="ft"> int32</tspan></text>
<text x="31" y="84" class="fn">create_time<tspan class="ft"> Timestamp</tspan></text>
<text x="31" y="100" class="fn">wish<tspan class="ft"> Wish</tspan></text>
<text x="31" y="116" class="fn">projects[]<tspan class="ft"> ProjectRef</tspan></text>
<text x="269" y="116" class="ff" text-anchor="end">◆</text>
<text x="31" y="132" class="fn">tasks[] · events[]<tspan class="ft"> </tspan></text>
<text x="31" y="148" class="fn">questions[] · blocks[]<tspan class="ft"> </tspan></text>
<text x="31" y="164" class="fn">commands[]<tspan class="ft"> Command</tspan></text>
<text x="269" y="164" class="ff" text-anchor="end">◆</text>
</g>
<g class="d-projects">
<rect x="330" y="20" width="220" height="120" rx="9" fill="var(--raise)" stroke="var(--c)" stroke-width="1"/>
<text x="341" y="40" class="bt">ProjectRef</text>
<line x1="330" x2="550" y1="50" y2="50" stroke="var(--c)" stroke-opacity="0.45"/>
<text x="341" y="68" class="fn">id<tspan class="ft"> string</tspan></text>
<text x="341" y="84" class="fn">name<tspan class="ft"> string</tspan></text>
<text x="341" y="100" class="fn">remote<tspan class="ft"> string</tspan></text>
<text x="341" y="116" class="fn">git<tspan class="ft"> bool</tspan></text>
</g>
<g class="d-machine">
<rect x="600" y="20" width="220" height="140" rx="9" fill="var(--raise)" stroke="var(--c)" stroke-width="1"/>
<text x="611" y="40" class="bt">Command</text>
<line x1="600" x2="820" y1="50" y2="50" stroke="var(--c)" stroke-opacity="0.45"/>
<text x="611" y="68" class="fn">id<tspan class="ft"> string</tspan></text>
<text x="611" y="84" class="fn">actor<tspan class="ft"> string</tspan></text>
<text x="611" y="100" class="fn">at<tspan class="ft"> Timestamp</tspan></text>
<text x="611" y="116" class="fn">method<tspan class="ft"> string</tspan></text>
<text x="611" y="132" class="fn">request<tspan class="ft"> Any</tspan></text>
</g>
</svg>`
)

func wrapSVG(svg string) string {
	return "```svg\n" + strings.TrimSpace(svg) + "\n```"
}

func extractSVG(html, id string) (string, error) {
	re := regexp.MustCompile(`(?s)(<svg[^>]*id=["']` + id + `["'][^>]*>.*?</svg>)`)
	m := re.FindStringSubmatch(html)
	if len(m) < 2 {
		return "", fmt.Errorf("failed to find SVG %s in tilasm HTML", id)
	}
	return m[1], nil
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	ctx := context.Background()
	wishFlag := flag.String("wish", "01a11833-a440-7479-a067-52615c91da70", "target wish id")
	dbFlag := flag.String("db", filepath.Join(os.Getenv("HOME"), ".config/djinn/djinn.db"), "path to djinn database")
	tilasmFlag := flag.String("tilasm", filepath.Join(os.Getenv("HOME"), ".config/djinn/tilasms/01a123b8-b048-753d-9bee-aaaafa421767/v4/index.html"), "path to tilasm index.html")
	flag.Parse()

	htmlBytes, err := os.ReadFile(*tilasmFlag)
	if err != nil {
		return fmt.Errorf("read tilasm index.html: %w", err)
	}
	tilasmHTML := string(htmlBytes)

	diaTables, err := extractSVG(tilasmHTML, "dia-tables")
	if err != nil {
		return err
	}
	diaFlux, err := extractSVG(tilasmHTML, "dia-flux")
	if err != nil {
		return err
	}
	diaWish, err := extractSVG(tilasmHTML, "dia-wish")
	if err != nil {
		return err
	}
	diaBlocks, err := extractSVG(tilasmHTML, "dia-blocks")
	if err != nil {
		return err
	}
	diaQuestion, err := extractSVG(tilasmHTML, "dia-question")
	if err != nil {
		return err
	}
	diaTask, err := extractSVG(tilasmHTML, "dia-task")
	if err != nil {
		return err
	}
	diaProjects, err := extractSVG(tilasmHTML, "dia-projects")
	if err != nil {
		return err
	}
	diaMachine, err := extractSVG(tilasmHTML, "dia-machine")
	if err != nil {
		return err
	}
	diaJournal, err := extractSVG(tilasmHTML, "dia-journal")
	if err != nil {
		return err
	}

	db, err := sql.Open("sqlite", "file:"+*dbFlag+"?mode=ro")
	if err != nil {
		return fmt.Errorf("open sqlite: %w", err)
	}
	defer db.Close()

	loadContent := func(blockID string) (string, error) {
		row := db.QueryRowContext(ctx, "SELECT payload FROM block WHERE id = ?", blockID)
		var payload []byte
		if err := row.Scan(&payload); err != nil {
			return "", fmt.Errorf("scan block %s: %w", blockID, err)
		}
		var b planv1.Block
		if err := proto.Unmarshal(payload, &b); err != nil {
			return "", fmt.Errorf("unmarshal block %s: %w", blockID, err)
		}
		return b.GetContent(), nil
	}

	updateBlock := func(blockID, newContent string) error {
		cmd := exec.CommandContext(ctx, "djinn", "block", "put", *wishFlag, "--id", blockID, "--content", newContent)
		cmd.Env = os.Environ()
		out, err := cmd.CombinedOutput()
		if err != nil {
			return fmt.Errorf("update block %s failed: %w (output: %s)", blockID, err, strings.TrimSpace(string(out)))
		}
		fmt.Printf("Updated block %s (%d bytes)\n", blockID, len(newContent))
		return nil
	}

	// Block 1: Overview tables
	{
		id := "01a11fe0-8f2e-798d-b9e8-ca00fb72bf9e"
		content, err := loadContent(id)
		if err != nil {
			return err
		}
		newContent := reMermaid.ReplaceAllString(content, wrapSVG(diaTables))
		if err := updateBlock(id, newContent); err != nil {
			return err
		}
	}

	// Block 2: Flux
	{
		id := "01a120fa-1c78-7153-a737-591e6adb91c2"
		content, err := loadContent(id)
		if err != nil {
			return err
		}
		newContent := reMermaid.ReplaceAllString(content, wrapSVG(diaFlux))
		if err := updateBlock(id, newContent); err != nil {
			return err
		}
	}

	// Block 3: Wish and Block
	{
		id := "01a120fa-1cf5-76eb-af68-3f18e0037d80"
		content, err := loadContent(id)
		if err != nil {
			return err
		}
		replacement := wrapSVG(diaWish) + "\n\n" + wrapSVG(diaBlocks)
		newContent := reMermaid.ReplaceAllString(content, replacement)
		if err := updateBlock(id, newContent); err != nil {
			return err
		}
	}

	// Block 4: Questions, Decisions and Routes
	{
		id := "01a120fa-1dcb-7a70-a6b8-2baf0e186f99"
		content, err := loadContent(id)
		if err != nil {
			return err
		}
		locs := reMermaid.FindAllStringIndex(content, -1)
		if len(locs) != 2 {
			return fmt.Errorf("expected 2 mermaid blocks in %s, found %d", id, len(locs))
		}
		var sb strings.Builder
		sb.WriteString(content[:locs[0][0]])
		sb.WriteString(wrapSVG(diaQuestion))
		sb.WriteString(content[locs[0][1]:locs[1][0]])
		sb.WriteString(wrapSVG(diaRouteSVG))
		sb.WriteString(content[locs[1][1]:])
		if err := updateBlock(id, sb.String()); err != nil {
			return err
		}
	}

	// Block 5: Tasks, Events, Closures, Usage
	{
		id := "01a120fa-1e38-77f6-ae55-18b38351de2d"
		content, err := loadContent(id)
		if err != nil {
			return err
		}
		newContent := reMermaid.ReplaceAllString(content, wrapSVG(diaTask))
		if err := updateBlock(id, newContent); err != nil {
			return err
		}
	}

	// Block 6: Projects and Skills
	{
		id := "01a120fa-1eb7-797c-a16b-2ec4b71ec1ce"
		content, err := loadContent(id)
		if err != nil {
			return err
		}
		locs := reMermaid.FindAllStringIndex(content, -1)
		if len(locs) != 2 {
			return fmt.Errorf("expected 2 mermaid blocks in %s, found %d", id, len(locs))
		}
		var sb strings.Builder
		sb.WriteString(content[:locs[0][0]])
		sb.WriteString(wrapSVG(diaProjects))
		sb.WriteString(content[locs[0][1]:locs[1][0]])
		sb.WriteString(wrapSVG(diaSkillSVG))
		sb.WriteString(content[locs[1][1]:])
		if err := updateBlock(id, sb.String()); err != nil {
			return err
		}
	}

	// Block 7: Machine, Gates and Journal
	{
		id := "01a120fa-1f37-7eeb-96cd-59021d22eab6"
		content, err := loadContent(id)
		if err != nil {
			return err
		}
		replacement := wrapSVG(diaMachine) + "\n\n" + wrapSVG(diaJournal)
		newContent := reMermaid.ReplaceAllString(content, replacement)
		if err := updateBlock(id, newContent); err != nil {
			return err
		}
	}

	// Block 8: Settings and Export
	{
		id := "01a120fa-1fab-75e2-ad8a-2df604c3e8bc"
		content, err := loadContent(id)
		if err != nil {
			return err
		}
		locs := reMermaid.FindAllStringIndex(content, -1)
		if len(locs) != 2 {
			return fmt.Errorf("expected 2 mermaid blocks in %s, found %d", id, len(locs))
		}
		var sb strings.Builder
		sb.WriteString(content[:locs[0][0]])
		sb.WriteString(wrapSVG(diaSettingsSVG))
		sb.WriteString(content[locs[0][1]:locs[1][0]])
		sb.WriteString(wrapSVG(diaExportSVG))
		sb.WriteString(content[locs[1][1]:])
		if err := updateBlock(id, sb.String()); err != nil {
			return err
		}
	}

	fmt.Println("Successfully redrew all 8 diagram blocks into SVG.")
	return nil
}
