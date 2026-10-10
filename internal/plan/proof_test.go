package plan

import (
	"slices"
	"testing"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
)

// TestReadDoneWhen: the Done-when section is read on the shapes of the real plan files: top-level boxes over several
// lines, boxes nested in one, a struck box, a section without boxes left, a file without the section (T09), a
// parenthesis that holds needs without opening on them (T16), a file that merged azimas, one section in each part
// (T02). An unchecked box without needs is work: the azima gets no needs.
func TestReadDoneWhen(t *testing.T) {
	for _, c := range []struct {
		name               string
		body               string
		found              bool
		checked, unchecked int
		// The boxes and their needs, when nothing but proof is left.
		needs [][2]string
	}{
		{
			name: "top-level boxes over several lines, as T01",
			body: "# T01 · The native window\n\nWhat.\n\n## Done when\n" +
				"- [ ] `djinn up` opens the window on Linux and macOS, rendering compared. (needs: a Mac, and a person to compare\n" +
				"  the rendering with Linux)\n" +
				"- [x] A server stream reaches the window under WebKitGTK (`check-window`, 08/10).\n" +
				"- [ ] Same under WKWebView (macOS) and WebView2 (Windows). (needs: `go tool task check-window` on a Mac and on\n" +
				"  Windows)\n" +
				"\n## Open questions\n- [ ] Not a box of the section.\n",
			found: true, checked: 1, unchecked: 2,
			needs: [][2]string{
				{"`djinn up` opens the window on Linux and macOS, rendering compared.", "a Mac, and a person to compare the rendering with Linux"},
				{"Same under WKWebView (macOS) and WebView2 (Windows).", "`go tool task check-window` on a Mac and on Windows"},
			},
		},
		{
			name: "boxes nested in a box without needs of its own, as T19",
			body: "## Done when\n" +
				"- [ ] A tag produces binaries. (needs: a maintainer to push a `v*` tag; `release.yml` exists, never run)\n" +
				"- [ ] macOS gets `Djinn.app`, a bundle with an identifier: Finder, Launchpad, the Dock's icon and the system\n" +
				"  notifications need it.\n" +
				"  - [x] `tools/macapp` lays out the bundle. (`TestBundleLayout`,\n" +
				"    `TestZipApp`)\n" +
				"  - [ ] Opened on a Mac from Finder. (needs: a Mac)\n",
			found: true, checked: 1, unchecked: 3,
			needs: [][2]string{
				{"A tag produces binaries.", "a maintainer to push a `v*` tag; `release.yml` exists, never run"},
				{"Opened on a Mac from Finder.", "a Mac"},
			},
		},
		{
			name: "a nested box without needs is work",
			body: "## Done when\n- [ ] The icon in the system.\n  - [x] Linux.\n  - [ ] Windows: the resource is built.\n" +
				"  - [ ] macOS. (needs: a Mac)\n",
			found: true, checked: 1, unchecked: 3,
		},
		{
			name:  "a box whose nested boxes are all checked is work",
			body:  "## Done when\n- [ ] Notifications.\n  - [x] On Linux.\n",
			found: true, checked: 1, unchecked: 1,
		},
		{
			name: "a struck box no longer counts, as T03",
			body: "## Done when\n- [x] It starts.\n- [ ] ~~No React file changed to get there.~~ Superseded by Q23.\n" +
				"- [ ] Clément has reviewed the switch. (needs: Clément's review)\n",
			found: true, checked: 1, unchecked: 1,
			needs: [][2]string{{"Clément has reviewed the switch.", "Clément's review"}},
		},
		{
			name: "needs inside a parenthesis that opens on something else, as T16, are work",
			body: "## Done when\n- [ ] The benchmark runs. (Go side done: `go tool task bench-dispatch`; needs: Ollama with\n" +
				"  Gemma 4 on the machine)\n- [ ] A decision is written down. (needs: the bench, then a person to decide)\n",
			found: true, unchecked: 2,
		},
		{
			name:  "a parenthesis left open is work",
			body:  "## Done when\n- [ ] Seen on a Mac. (needs: a Mac\n",
			found: true, unchecked: 1,
		},
		{
			name:  "every box checked, as T17 said in-progress",
			body:  "## Done when\n- [x] One. (`TestOne`)\n- [x] Two.\n  - [X] Nested.\n## Open questions\n",
			found: true, checked: 3,
		},
		{
			name: "a section in each merged part counts, as T02",
			body: "# T02 · The foundation\n\n## Done when\n- [x] Generated. (`TestGen`)\n\n## Open questions\n- [ ] Not a box.\n\n" +
				"## From T08 · Data\n\n### Decided\n- [ ] Not a box either.\n\n### Done when\n- [x] Stored.\n" +
				"- [ ] Seen on Windows. (needs: a Windows machine)\n\n#### Detail\n- [x] Nested deeper.\n\n" +
				"## From T09 · Quality of life\n\n- [ ] Outside any section.\n",
			found: true, checked: 3, unchecked: 1,
			needs: [][2]string{{"Seen on Windows.", "a Windows machine"}},
		},
		{
			name: "no Done-when section, as T09",
			body: "# T09 · Quality of life\n\n- [ ] **Djinn's own icon in the system.**\n" +
				"  - [ ] Seen in GNOME's Activities. (needs: a person on GNOME)\n",
		},
		{
			name:  "a deeper heading inside the section ends nothing; text under no box ends the box",
			body:  "### Done when\n- [ ] Pressed on a Mac.\nA note.\n  (needs: a Mac)\n#### Detail\n- [ ] Seen. (needs: a person)\n",
			found: true, unchecked: 2,
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			d := ReadDoneWhen([]byte(c.body))
			if d.Found != c.found || d.Checked != c.checked || d.Unchecked != c.unchecked {
				t.Errorf("found %v, %d checked, %d unchecked; want %v, %d, %d", d.Found, d.Checked, d.Unchecked, c.found,
					c.checked, c.unchecked)
			}
			var got [][2]string
			for _, n := range d.Needs {
				got = append(got, [2]string{n.GetBox(), n.GetNeeds()})
			}
			if !slices.Equal(got, c.needs) {
				t.Errorf("needs %q\nwant %q", got, c.needs)
			}
			if all := c.found && c.checked > 0 && c.unchecked == 0; d.AllChecked() != all {
				t.Errorf("all checked: %v", d.AllChecked())
			}
		})
	}
}

// TestProvers: who gives a proof is read from the plain words of its needs, as the plan files write them.
func TestProvers(t *testing.T) {
	const (
		mac, windows, review = planv1.Prover_PROVER_MAC, planv1.Prover_PROVER_WINDOWS, planv1.Prover_PROVER_REVIEW
		release, model       = planv1.Prover_PROVER_RELEASE, planv1.Prover_PROVER_REAL_MODEL
		person, other        = planv1.Prover_PROVER_PERSON, planv1.Prover_PROVER_OTHER
	)
	for needs, want := range map[string][]planv1.Prover{
		"a Mac, and a person to compare the rendering with Linux": {mac, person},
		"`go tool task check-window` on a Mac and on Windows":     {mac, windows},
		"Clément's review": {review},
		"a published release, then a person on each system":                {release, person},
		"a maintainer to push a `v*` tag; `release.yml` exists":            {release, person},
		"a person and a real model":                                        {model, person},
		"the paid bench, `go tool task bench-workers`, with a person's go": {model, person},
		"a real lead session":                                              {model},
		"two people":                                                       {person},
		"a Windows 11 machine":                                             {windows},
		"a person with a microphone, on Linux and on a Mac":                {mac, person},
		"after v1; two trusted machines":                                   {other},
		"Macs and windowsill":                                              {other},
	} {
		got, _ := Provers(needs)
		if !slices.Equal(got, want) {
			t.Errorf("%q: %v, want %v", needs, got, want)
		}
	}
	if _, who := Provers("Clément's review"); who != "Clément" {
		t.Errorf("reviewer %q", who)
	}
	if _, who := Provers("a review of the layout"); who != "" {
		t.Errorf("reviewer %q", who)
	}
	n := func(needs string) *planv1.ProofNeed {
		provers, who := Provers(needs)
		return &planv1.ProofNeed{Needs: needs, Provers: provers, Reviewer: who}
	}
	if got := NeedsWords([]*planv1.ProofNeed{n("a person and a real model"), n("Clément's review"), n("a Mac"),
		n("after v1; nothing built")}); got != "a Mac, Clément's review, a real model, a person, after v1; nothing built" {
		t.Errorf("words: %s", got)
	}
	if !GivenByAPerson(n("Clément's review")) || !GivenByAPerson(n("two people")) || GivenByAPerson(n("a Mac")) {
		t.Error("given by a person")
	}
}
