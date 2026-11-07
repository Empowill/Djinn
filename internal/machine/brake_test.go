package machine

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"connectrpc.com/connect"

	djinnv1 "github.com/empowill/djinn/gen/go/djinn/v1"
)

// TestReadBrake: the threshold settings.json says, 0 included; none when the file is missing, broken, silent on it,
// or says more than 95.
func TestReadBrake(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, settings string
		percent        int
		ok             bool
	}{
		{name: "no file"},
		{name: "broken file", settings: `{"brakeMemoryPercent":`},
		{name: "silent", settings: `{"load":"LOAD_NOTCH_HIGH"}`},
		{name: "off", settings: `{"brakeMemoryPercent":0}`, ok: true},
		{name: "8", settings: `{"brakeMemoryPercent":8}`, percent: 8, ok: true},
		{name: "past 95", settings: `{"brakeMemoryPercent":96}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			percent, ok := ReadBrake(settingsHome(t, tc.settings))
			if percent != tc.percent || ok != tc.ok {
				t.Errorf("ReadBrake = %d, %v; want %d, %v", percent, ok, tc.percent, tc.ok)
			}
		})
	}
}

// TestLoadService_SetBrake: by default Get shows 5 %; SetBrake keeps the threshold in settings.json beside the other
// settings, gives it to the brake at once, and Get shows it; 0 turns it off; past 95 is refused, and so is any
// change while djinn up's flag fixes it.
func TestLoadService_SetBrake(t *testing.T) {
	t.Parallel()
	home := settingsHome(t, `{"load":"LOAD_NOTCH_HIGH"}`)
	client, _ := serveLoad(t, home, nil, nil, nil, nil, WithLoadTickInterval(0))
	get := func() *djinnv1.LoadServiceGetResponse {
		t.Helper()
		res, err := client.Get(t.Context(), connect.NewRequest(&djinnv1.LoadServiceGetRequest{}))
		if err != nil {
			t.Fatal(err)
		}
		return res.Msg
	}
	if got := get(); got.GetBrakeMemoryPercent() != 5 || got.GetBrakeMemoryFixed() || got.GetBrakeOff() {
		t.Fatalf("by default: %d%%, fixed %v, off %v; want 5%%, not fixed, on", got.GetBrakeMemoryPercent(), got.GetBrakeMemoryFixed(), got.GetBrakeOff())
	}

	var applied []int
	client, _ = serveLoad(t, home, nil, nil, nil, nil, WithLoadTickInterval(0),
		WithLoadBrake(BrakeSetting{Percent: 5, Apply: func(p int) { applied = append(applied, p) }}))
	var setRes *djinnv1.LoadServiceSetBrakeResponse
	set := func(p uint32) error {
		res, err := client.SetBrake(t.Context(), connect.NewRequest(&djinnv1.LoadServiceSetBrakeRequest{Percent: &p}))
		if err == nil {
			setRes = res.Msg
		}
		return err
	}
	for _, p := range []uint32{8, 0} {
		if err := set(p); err != nil {
			t.Fatalf("SetBrake %d: %v", p, err)
		}
		got := get()
		if got.GetBrakeMemoryPercent() != int32(p) {
			t.Errorf("after SetBrake %d, Get shows %d", p, got.GetBrakeMemoryPercent())
		}
		// The command line prints no field at zero: brake_off says the brake is off where the percent cannot.
		if off := p == 0; got.GetBrakeOff() != off || setRes.GetBrakeOff() != off {
			t.Errorf("after SetBrake %d, Get says off %v, SetBrake says off %v; want %v", p, got.GetBrakeOff(), setRes.GetBrakeOff(), off)
		}
		if saved, ok := ReadBrake(home); !ok || saved != int(p) {
			t.Errorf("after SetBrake %d, settings.json says %d, %v", p, saved, ok)
		}
	}
	if len(applied) != 2 || applied[0] != 8 || applied[1] != 0 {
		t.Errorf("the brake was given %v, want [8 0]", applied)
	}
	if ReadNotch(home) != djinnv1.LoadNotch_LOAD_NOTCH_HIGH {
		t.Error("SetBrake lost the notch saved beside it")
	}
	if err := set(96); connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Errorf("SetBrake 96: %v, want invalid_argument", err)
	}
	if _, err := client.SetBrake(t.Context(), connect.NewRequest(&djinnv1.LoadServiceSetBrakeRequest{})); connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Errorf("SetBrake without a percent: %v, want invalid_argument", err)
	}

	fixed := settingsHome(t, "")
	client, _ = serveLoad(t, fixed, nil, nil, nil, nil, WithLoadTickInterval(0),
		WithLoadBrake(BrakeSetting{Percent: 12, Fixed: true, Apply: func(p int) { applied = append(applied, p) }}))
	if got := get(); got.GetBrakeMemoryPercent() != 12 || !got.GetBrakeMemoryFixed() {
		t.Errorf("fixed by the flag: %d%%, fixed %v; want 12%%, fixed", got.GetBrakeMemoryPercent(), got.GetBrakeMemoryFixed())
	}
	err := set(5)
	if connect.CodeOf(err) != connect.CodeFailedPrecondition || !strings.Contains(err.Error(), "--brake-memory") {
		t.Errorf("SetBrake while fixed: %v, want failed_precondition naming --brake-memory", err)
	}
	if _, err := os.Stat(filepath.Join(fixed, settingsFile)); !os.IsNotExist(err) {
		t.Errorf("SetBrake while fixed wrote settings.json: %v", err)
	}
	if len(applied) != 2 {
		t.Errorf("SetBrake while fixed reached the brake: %v", applied)
	}
}
