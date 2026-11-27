package enrollment_test

import (
	"github.com/victorarias/attn/internal/enrollment"
	"strings"
	"testing"
)

func TestHomePairingRefusesASecondHome(t *testing.T) {
	for _, ensured := range []bool{false, true} {
		t.Run(map[bool]string{false: "fresh", true: "standalone"}[ensured], func(t *testing.T) {
			root := t.TempDir()
			own, err := enrollment.EnsureDaemonID(root)
			if err != nil {
				t.Fatal(err)
			}
			if ensured {
				if _, err := enrollment.Ensure(root, own); err != nil {
					t.Fatal(err)
				}
			}
			const first = "h-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
			const second = "h-bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
			const firstDaemon = "d-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
			for range 2 {
				if _, err := enrollment.RecordHome(root, enrollment.Home{ID: first, DaemonID: firstDaemon, Name: "first"}); err != nil {
					t.Fatal(err)
				}
			}
			status, err := enrollment.Load(root)
			if err != nil || status.HomeID != first || !status.IsOutpost() {
				t.Fatalf("paired: %+v %v", status, err)
			}
			_, err = enrollment.RecordHome(root, enrollment.Home{ID: second, DaemonID: firstDaemon, Name: "second"})
			if err == nil || !strings.Contains(err.Error(), first) || !strings.Contains(err.Error(), second) || !strings.Contains(err.Error(), "attn enrollment leave") {
				t.Fatalf("second home: %v", err)
			}
			status, err = enrollment.Ensure(root, own)
			if err != nil || status.HomeID != first || !status.IsOutpost() {
				t.Fatalf("restart: %+v %v", status, err)
			}
		})
	}
}
