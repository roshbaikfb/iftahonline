package domain_test

import (
	"testing"

	"github.com/roshbaikfb/iftahonline/internal/modules/risk/internal/domain"
	"github.com/roshbaikfb/iftahonline/internal/platform/errs"
)

func TestRulesDecide(t *testing.T) {
	t.Parallel()
	const (
		off = domain.Off
		med = domain.BandMedium
		hi  = domain.BandHigh
	)
	tests := []struct {
		rules domain.Rules
		band  domain.Band
		want  domain.Action
	}{
		{domain.Rules{}, hi, domain.NoAction},
		{domain.Rules{HoldFrom: off, VerifyFrom: off}, hi, domain.NoAction},
		{domain.Rules{HoldFrom: hi}, hi, domain.Hold},
		{domain.Rules{HoldFrom: hi}, med, domain.NoAction},
		{domain.Rules{HoldFrom: med}, hi, domain.Hold},
		{domain.Rules{VerifyFrom: med}, med, domain.Verify},
		{domain.Rules{VerifyFrom: med}, domain.BandLow, domain.NoAction},
		{domain.Rules{HoldFrom: hi, VerifyFrom: med}, hi, domain.Hold}, // a hold first
		{domain.Rules{HoldFrom: hi, VerifyFrom: med}, med, domain.Verify},
		{domain.Rules{HoldFrom: med, VerifyFrom: med}, domain.BandNew, domain.NoAction}, // no history is no risk
		{domain.Rules{PrepayFrom: hi}, hi, domain.Prepay},
		{domain.Rules{PrepayFrom: hi, VerifyFrom: med}, med, domain.Verify},
		{domain.Rules{HoldFrom: hi, PrepayFrom: med, VerifyFrom: med}, hi, domain.Hold},
		{domain.Rules{HoldFrom: hi, PrepayFrom: med, VerifyFrom: med}, med, domain.Prepay}, // a prepay before a call
	}
	for _, tt := range tests {
		if got := tt.rules.Decide(tt.band); got != tt.want {
			t.Errorf("%+v on %s = %q, want %q", tt.rules, tt.band, got, tt.want)
		}
	}
	if (domain.Rules{}).Active() || !(domain.Rules{VerifyFrom: hi}).Active() || !(domain.Rules{PrepayFrom: med}).Active() {
		t.Error("Active")
	}
}

func TestRulesValidate(t *testing.T) {
	t.Parallel()
	for _, ok := range []domain.Rules{{HoldFrom: domain.Off, VerifyFrom: domain.Off, PrepayFrom: domain.Off},
		{HoldFrom: domain.BandHigh, VerifyFrom: domain.BandMedium, PrepayFrom: domain.BandHigh}} {
		if err := ok.Validate(); err != nil {
			t.Errorf("%+v: %v", ok, err)
		}
	}
	for _, bad := range []domain.Rules{{HoldFrom: domain.BandLow, VerifyFrom: domain.Off, PrepayFrom: domain.Off},
		{HoldFrom: domain.Off, VerifyFrom: domain.BandNew, PrepayFrom: domain.Off}, {HoldFrom: domain.Off, VerifyFrom: domain.Off, PrepayFrom: "x"}, {}} {
		if err := bad.Validate(); !errs.Is(err, errs.KindValidation) {
			t.Errorf("%+v: %v", bad, err)
		}
	}
}
