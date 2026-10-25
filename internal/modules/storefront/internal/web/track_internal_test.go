package web

import (
	"strings"
	"testing"
)

func TestTrackSteps(t *testing.T) {
	t.Parallel()
	tests := []struct {
		status, parcel string
		want           string // the five steps: d done, n now, - to come
		alert          Msg
	}{
		{"pending", "", "dn---", 0},
		{"confirmed", "", "ddn--", 0},
		{"ready_to_ship", "created", "ddn--", 0},
		{"shipped", "in_transit", "dddn-", 0},
		{"shipped", "out_for_delivery", "ddddn", 0},
		{"delivered", "delivered", "ddddd", 0},
		{"cancelled", "", "d----", MsgTrackCancelled},
		{"failed_delivery", "failed_attempt", "dddd-", MsgTrackFailed},
		{"returned", "returned", "ddd--", MsgTrackReturned},
	}
	for _, tt := range tests {
		tr := Tracking{Status: tt.status}
		if tt.parcel != "" {
			tr.Parcel = &Parcel{Status: tt.parcel}
		}
		steps, alert := trackSteps(tr)
		var b strings.Builder
		for _, s := range steps {
			b.WriteString(map[string]string{"done": "d", "now": "n", "": "-"}[s.state])
		}
		if b.String() != tt.want || alert != tt.alert {
			t.Errorf("%s/%s: %s alert %d, want %s alert %d", tt.status, tt.parcel, b.String(), alert, tt.want, tt.alert)
		}
	}
}
