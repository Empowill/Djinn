package query

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/apache/arrow-go/v18/arrow"
	"github.com/apache/arrow-go/v18/arrow/array"
	"github.com/apache/arrow-go/v18/arrow/ipc"
)

// readArrow reads back the one record batch of an Arrow IPC stream.
func readArrow(t *testing.T, b []byte) arrow.RecordBatch {
	t.Helper()
	r, err := ipc.NewReader(bytes.NewReader(b))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(r.Release)
	if !r.Next() {
		t.Fatalf("no record batch: %v", r.Err())
	}
	rec := r.RecordBatch()
	rec.Retain()
	t.Cleanup(rec.Release)
	if r.Next() {
		t.Error("a second record batch")
	}
	return rec
}

// metaOf reads the "djinn" metadata of a field.
func metaOf(t *testing.T, f arrow.Field) Meta {
	t.Helper()
	raw, ok := f.Metadata.GetValue(MetaKey)
	if !ok {
		t.Fatalf("field %s has no %s metadata", f.Name, MetaKey)
	}
	var m Meta
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		t.Fatal(err)
	}
	return m
}

func TestArrowRoundTrip(t *testing.T) {
	t.Parallel()
	c, _ := catalog(t)
	selects := []string{"code", "status", "usage.input_tokens", "usage.cost_usd", "urgent", "create_time", "running",
		"depends_on", "prompt::size", "wish_id->title"}
	table, err := c.Run(t.Context(), Query{Collection: "TASKS", Selects: selects})
	if err != nil {
		t.Fatal(err)
	}
	b, err := table.Arrow()
	if err != nil {
		t.Fatal(err)
	}
	rec := readArrow(t, b)
	if rec.NumRows() != 4 || int(rec.NumCols()) != len(selects) {
		t.Fatalf("%d rows x %d columns; want 4 x %d", rec.NumRows(), rec.NumCols(), len(selects))
	}

	// The type comes from the descriptor: running is a duration though only T1 holds one.
	wantTypes := []string{"utf8", "dictionary<values=utf8, indices=int32, ordered=false>", "int64", "float64", "bool",
		"timestamp[ms, tz=UTC]", "duration[ms]", "list<item: utf8, nullable>", "int64", "utf8"}
	for i, f := range rec.Schema().Fields() {
		if f.Name != selects[i] || f.Type.String() != wantTypes[i] || !f.Nullable {
			t.Errorf("field %d = %s %s nullable %v; want %s %s", i, f.Name, f.Type, f.Nullable, selects[i], wantTypes[i])
		}
	}
	for i, want := range []Meta{
		{Ref: "code", Label: "Code", Type: "string"},
		{Ref: "status", Label: "Status", Type: "enum"},
		{Ref: "usage.input_tokens", Label: "Usage input tokens", Unit: "tokens", Type: "int"},
		{Ref: "usage.cost_usd", Label: "Usage cost usd", Unit: "usd", Type: "float"},
	} {
		if got := metaOf(t, rec.Schema().Field(i)); got.Ref != want.Ref || got.Label != want.Label ||
			got.Unit != want.Unit || got.Type != want.Type {
			t.Errorf("meta %d = %+v; want %+v", i, got, want)
		}
	}
	if m := metaOf(t, rec.Schema().Field(8)); m.Unit != "tokens" || m.Label != "Prompt size" {
		t.Errorf("prompt::size meta = %+v; want tokens, Prompt size", m)
	}
	if m := metaOf(t, rec.Schema().Field(9)); m.Label != "Wish title" {
		t.Errorf("wish_id->title meta = %+v; want Wish title", m)
	}

	// Rows in the source's order: T1, T2, T3, T4.
	codes := rec.Column(0).(*array.String)
	if codes.Value(0) != "W1" || codes.Value(3) != "W3" {
		t.Errorf("codes = %v", codes)
	}
	status := rec.Column(1).(*array.Dictionary)
	names := status.Dictionary().(*array.String)
	if got := names.Value(status.GetValueIndex(0)); got != "TASK_STATUS_DONE" || !status.IsNull(2) {
		t.Errorf("status = %v; want TASK_STATUS_DONE first, T3's null", status)
	}
	// Null is never 0: T2's usage has no cost yet (0, a value), T3 has no usage at all (null).
	cost := rec.Column(3).(*array.Float64)
	if cost.Value(0) != 1.5 || cost.IsNull(1) || cost.Value(1) != 0 || !cost.IsNull(2) || cost.Value(3) != 0.25 {
		t.Errorf("cost = %v; want [1.5 0 (null) 0.25]", cost)
	}
	if urgent := rec.Column(4).(*array.Boolean); !urgent.Value(2) || urgent.Value(0) {
		t.Errorf("urgent = %v", urgent)
	}
	created := rec.Column(5).(*array.Timestamp)
	if got := created.Value(1).ToTime(arrow.Millisecond); !got.Equal(day0.Add(24 * time.Hour)) {
		t.Errorf("T2 create_time = %v", got)
	}
	running := rec.Column(6).(*array.Duration)
	if running.Value(0) != arrow.Duration((90*time.Minute).Milliseconds()) || !running.IsNull(1) {
		t.Errorf("running = %v; want 90 min in ms, then null", running)
	}
	deps := rec.Column(7).(*array.List)
	if !deps.IsNull(0) || deps.IsNull(1) {
		t.Errorf("depends_on = %v; want null for T1, T1's id for T2", deps)
	}
	if start, end := deps.ValueOffsets(1); end-start != 1 || deps.ListValues().(*array.String).Value(int(start)) != id(4) {
		t.Errorf("T2 depends_on = %v", deps)
	}
	if size := rec.Column(8).(*array.Int64); size.Value(0) != 4 || !size.IsNull(1) {
		t.Errorf("prompt::size = %v; want 4 (13 letters), then null", size)
	}
	if titles := rec.Column(9).(*array.String); titles.Value(1) != "Stats" || !titles.IsNull(3) {
		t.Errorf("wish titles = %v; want Stats, null for T4's missing wish", titles)
	}
}

func TestArrowAggregated(t *testing.T) {
	t.Parallel()
	c, _ := catalog(t)
	table := aggregate(t, c, Aggregation{
		By:       []GroupBy{{Ref: "create_time", Granularity: Day}, {Ref: "running", Bounds: []any{"0s", "10s", "1h"}}},
		Measures: []Measure{{Fn: Count}, {Ref: "usage.cost_usd", Fn: Median}, {Ref: "running", Fn: P90, Alias: "slow"}},
	})
	b, err := table.Arrow()
	if err != nil {
		t.Fatal(err)
	}
	rec := readArrow(t, b)
	fields := rec.Schema().Fields()
	for i, want := range []Meta{
		{Ref: "create_time::day", Label: "Create time by day", Type: "time", Of: "create_time", Granularity: "day"},
		{Ref: "running::bins", Label: "Running", Type: "duration", Of: "running",
			Bounds: []any{float64(0), float64(10_000), float64(3_600_000)}},
		{Ref: "count", Label: "Count", Type: "int", Fn: "count"},
		{Ref: "usage.cost_usd::median", Label: "Usage cost usd (median)", Unit: "usd", Type: "float",
			Of: "usage.cost_usd", Fn: "median"},
		{Ref: "slow", Label: "Slow", Type: "duration", Of: "running", Fn: "p90"},
	} {
		got := metaOf(t, fields[i])
		gotJSON, _ := json.Marshal(got)
		wantJSON, _ := json.Marshal(want)
		if string(gotJSON) != string(wantJSON) {
			t.Errorf("meta %d = %s; want %s", i, gotJSON, wantJSON)
		}
	}
	if got := fields[1].Type.String(); got != "duration[ms]" {
		t.Errorf("bins type = %s; want the ref's own, duration[ms]", got)
	}
	if rec.NumRows() != int64(len(table.Rows)) {
		t.Errorf("%d rows; want %d", rec.NumRows(), len(table.Rows))
	}
}

func TestArrowEmpty(t *testing.T) {
	t.Parallel()
	c, _ := catalog(t)
	table, err := c.Run(t.Context(), Query{Collection: "TASKS", Filters: []Filter{Equal{Ref: "code", Value: "none"}}})
	if err != nil {
		t.Fatal(err)
	}
	b, err := table.Arrow()
	if err != nil {
		t.Fatal(err)
	}
	if rec := readArrow(t, b); rec.NumRows() != 0 || rec.NumCols() != 3 {
		t.Errorf("%d rows x %d columns; want the schema, no row", rec.NumRows(), rec.NumCols())
	}
}

func TestTextAndCSV(t *testing.T) {
	t.Parallel()
	c, _ := catalog(t)
	table, err := c.Run(t.Context(), Query{Collection: "TASKS", Limit: 3,
		Selects: []string{"code", "status", "running", "usage.cost_usd", "depends_on", "create_time"}})
	if err != nil {
		t.Fatal(err)
	}
	var text bytes.Buffer
	if err := table.WriteText(&text); err != nil {
		t.Fatal(err)
	}
	want := `code  status   running  usage.cost_usd  depends_on                            create_time
W1    done     1h30m    1.5                                                   2026-11-02T09:00:00Z
W2    running           0               01900000-0000-7000-8000-000000000004  2026-11-03T09:00:00Z
W1                                                                            2026-11-04T09:00:00Z
3 of 4 rows
`
	if got := strings.ReplaceAll(text.String(), " \n", "\n"); trimLines(got) != trimLines(want) {
		t.Errorf("text:\n%s\nwant:\n%s", got, want)
	}

	var csvText bytes.Buffer
	if err := table.WriteCSV(&csvText); err != nil {
		t.Fatal(err)
	}
	wantCSV := "code,status,running,usage.cost_usd,depends_on,create_time\n" +
		"W1,done,1h30m,1.5,,2026-11-02T09:00:00Z\n" +
		"W2,running,,0,01900000-0000-7000-8000-000000000004,2026-11-03T09:00:00Z\n" +
		"W1,,,,,2026-11-04T09:00:00Z\n"
	if csvText.String() != wantCSV {
		t.Errorf("csv:\n%s\nwant:\n%s", csvText.String(), wantCSV)
	}

	bins := aggregate(t, c, Aggregation{By: []GroupBy{{Ref: "usage.cost_usd", Bounds: []any{0.5, 1, 2}}},
		Measures: []Measure{{Fn: Count}}})
	csvText.Reset()
	if err := bins.WriteCSV(&csvText); err != nil {
		t.Fatal(err)
	}
	if want := "usage.cost_usd::bins,count\n< 1,2\n1 to 2,1\n>= 2,0\n,1\n"; csvText.String() != want {
		t.Errorf("bins csv:\n%s\nwant:\n%s", csvText.String(), want)
	}
}

// trimLines drops the spaces that end each line: tabwriter pads the last column.
func trimLines(s string) string {
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		lines[i] = strings.TrimRight(l, " ")
	}
	return strings.Join(lines, "\n")
}

func TestDuration(t *testing.T) {
	t.Parallel()
	for d, want := range map[time.Duration]string{
		0: "0s", time.Hour: "1h", time.Minute: "1m", 90 * time.Minute: "1h30m", 61 * time.Second: "1m1s",
		250 * time.Millisecond: "250ms", time.Hour + 1500*time.Microsecond: "1h0m0.002s",
	} {
		if got := duration(d); got != want {
			t.Errorf("duration(%v) = %q; want %q", d, got, want)
		}
	}
}
