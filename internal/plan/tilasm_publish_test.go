package plan

import (
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"connectrpc.com/connect"

	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
)

// publisher is a Publisher whose claude replays the recorded publish of L52, and the log of its calls.
func publisher(t *testing.T) (*Publisher, string) {
	t.Helper()
	stream, err := filepath.Abs(filepath.Join("testdata", "artifact", "publish.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	path, log := fakeClaude(t, 0, stream, 0)
	return &Publisher{Home: t.TempDir(), UserHome: signedIn(t), Path: path}, log
}

// TestTilasmPublish: the first publish of a tilasm waits for the developer's go-ahead from the window; then the lead
// or a worker publishes it, and again on its address, each version after the last published waiting.
func TestTilasmPublish(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	p, log := publisher(t)
	c := serve(t, WithHome(home), WithPublisher(p))
	wish := c.wish(t)
	src := folder(t, map[string]string{
		"index.html":        page("The guide", "Screens of the window."),
		"img/one.png":       "png",
		"files/budget.xlsx": "xlsx",
	})
	first := putTilasm(t, c, &planv1.TilasmServicePutRequest{Path: src, Wish: wish}).GetTilasm()
	publish := func(wishID string) (*planv1.TilasmServicePublishResponse, error) {
		res, err := c.tilasms.Publish(t.Context(), connect.NewRequest(&planv1.TilasmServicePublishRequest{Tilasm: ref("L01"), Wish: wishID}))
		if err != nil {
			return nil, err
		}
		return res.Msg, nil
	}

	_, err := publish("")
	if code(err) != connect.CodeFailedPrecondition || !strings.Contains(err.Error(), "L01 waits for the developer") {
		t.Fatalf("first publish without the go-ahead = %v, want it refused, waiting for the developer", err)
	}
	if got := calls(t, log); len(got) != 0 {
		t.Fatalf("a refused publish called claude %d times", len(got))
	}

	confirm := func() *planv1.Tilasm {
		res, err := c.tilasms.ConfirmPublish(t.Context(), connect.NewRequest(&planv1.TilasmServiceConfirmPublishRequest{TilasmId: first.GetId()}))
		if err != nil {
			t.Fatal(err)
		}
		return res.Msg.GetTilasm()
	}
	confirmed := confirm().GetArtifact().GetConfirmTime()
	if confirmed == nil || confirm().GetArtifact().GetConfirmTime().AsTime() != confirmed.AsTime() {
		t.Fatalf("go-ahead = %v, want it kept from the first", confirmed)
	}

	res, err := publish("")
	if err != nil {
		t.Fatal(err)
	}
	url := "https://claude.ai/artifact/D1gnnFxekF5SvDnhTFSFcj"
	if res.GetUrl() != url || res.GetFiles() != 1 || !slices.Equal(res.GetLeftOut(), []string{"files/budget.xlsx"}) || res.GetCostUsd() != 0.01720977 {
		t.Errorf("publish = %v, want %s with img/one.png beside the page, the spreadsheet left out, and its cost", res, url)
	}
	art := getTilasm(t, c, "L01", 0).GetTilasm().GetArtifact()
	if art.GetUrl() != url || art.GetVersion() != 1 || art.GetArtifactVersion() != 2 || art.GetPublishTime() == nil ||
		art.GetConfirmTime().AsTime() != confirmed.AsTime() || art.GetCostUsd() != 0.01720977 {
		t.Errorf("artifact = %v, want its address, version 1 published, claude.ai's version 2, the go-ahead kept", art)
	}
	got := calls(t, log)
	if publishDir := got[len(got)-1][0]; !strings.HasPrefix(publishDir, filepath.Join(home, "tilasms", ".publish-")) {
		t.Errorf("claude ran in %s, want a folder of the publish in the tilasms' folder", publishDir)
	}
	if left, _ := filepath.Glob(filepath.Join(home, "tilasms", ".publish-*")); len(left) != 0 {
		t.Errorf("the publish left its folder: %v", left)
	}
	if prompt := strings.Join(got[len(got)-1], "\n"); strings.Contains(prompt, `"action": "read"`) {
		t.Errorf("a first publish read a live version: %s", prompt)
	}

	src2 := folder(t, map[string]string{"index.html": page("The guide", "More screens.")})
	putTilasm(t, c, &planv1.TilasmServicePutRequest{Path: src2, Wish: wish, Code: "L01"})
	if art := getTilasm(t, c, "L01", 0).GetTilasm().GetArtifact(); art.GetVersion() != 1 || art.GetUrl() != url {
		t.Errorf("after a new version, artifact = %v, want version 1 still published: version 2 waits", art)
	}
	if _, err := publish(wish); err != nil {
		t.Fatal(err)
	}
	got = calls(t, log)
	if prompt := strings.Join(got[len(got)-1], "\n"); !strings.Contains(prompt, `{"action": "read", "url": "`+url+`"}`) ||
		!strings.Contains(prompt, `"url":"`+url+`"`) {
		t.Errorf("a publish again reads the live version and publishes on its address: %s", prompt)
	}
	if art := getTilasm(t, c, "L01", 0).GetTilasm().GetArtifact(); art.GetVersion() != 2 {
		t.Errorf("artifact = %v, want version 2 published", art)
	}

	if _, err := publish(c.wish(t)); code(err) != connect.CodeNotFound && code(err) != connect.CodePermissionDenied {
		t.Errorf("publish in another wish = %v, want it refused", err)
	}

	exported := filepath.Join(t.TempDir(), "l01.zip")
	if _, err := c.tilasms.Export(t.Context(), connect.NewRequest(&planv1.TilasmServiceExportRequest{Tilasm: ref("L01"), File: exported})); err != nil {
		t.Fatal(err)
	}
	if manifest := zipped(t, exported)["tilasm.json"]; strings.Contains(manifest, "artifact") || strings.Contains(manifest, url) {
		t.Errorf("the export carries the artifact, the developer's own: %s", manifest)
	}
}

func TestTilasmPublishUnavailable(t *testing.T) {
	t.Parallel()
	c := serve(t, WithHome(t.TempDir()))
	wish := c.wish(t)
	put := putTilasm(t, c, &planv1.TilasmServicePutRequest{Path: folder(t, map[string]string{"index.html": page("A", "B")}), Wish: wish})
	if _, err := c.tilasms.ConfirmPublish(t.Context(), connect.NewRequest(&planv1.TilasmServiceConfirmPublishRequest{TilasmId: put.GetTilasm().GetId()})); err != nil {
		t.Fatal(err)
	}
	_, err := c.tilasms.Publish(t.Context(), connect.NewRequest(&planv1.TilasmServicePublishRequest{Tilasm: ref("L01")}))
	if code(err) != connect.CodeUnavailable {
		t.Errorf("publish without a publisher = %v, want unavailable", err)
	}
	res, err := c.tilasms.CheckPublish(t.Context(), connect.NewRequest(&planv1.TilasmServiceCheckPublishRequest{}))
	if err != nil {
		t.Fatal(err)
	}
	if res.Msg.GetAvailable() || res.Msg.GetBlock() != planv1.PublishBlock_PUBLISH_BLOCK_CHECK_FAILED {
		t.Errorf("check without a publisher = %v, want unavailable", res.Msg)
	}
}

func TestTilasmCheckPublish(t *testing.T) {
	t.Parallel()
	p, _ := publisher(t)
	c := serve(t, WithHome(t.TempDir()), WithPublisher(p))
	res, err := c.tilasms.CheckPublish(t.Context(), connect.NewRequest(&planv1.TilasmServiceCheckPublishRequest{}))
	if err != nil {
		t.Fatal(err)
	}
	if !res.Msg.GetAvailable() || res.Msg.GetBlock() != planv1.PublishBlock_PUBLISH_BLOCK_UNSPECIFIED {
		t.Errorf("check = %v, want available", res.Msg)
	}
}
