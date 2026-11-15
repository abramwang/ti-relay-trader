package performance

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
)

func attributionFixture(t *testing.T) CycleReviewSource {
	s := dailyCycleFixture(t)
	a := &AttributionReview{Schema: "relay.daily_attribution_bridge.v1", AccountID: s.AccountID, SourceSHA: strings.Repeat("d", 64), Sources: map[string]string{"synthetic": strings.Repeat("a", 64)}}
	p := AttributionPart{Date: "2026-10-12", CycleID: "2026-10-12/510310.SH", Kind: "redemption_source", Net: "3", OldNet: "0", Fee: "0", Correction: "3"}
	a.Parts = []AttributionPart{p}
	for i, d := range s.AccountDays {
		pub := float64(d.PublishedPnLCents) / 100
		other := float64(d.OtherClosingCents-d.OtherOpeningCents) / 100
		r := AttributionDay{Date: d.TradeDate, Status: "conditional_bridge_not_final", Published: fmt.Sprintf("%.2f", pub), Model: fmt.Sprintf("%.2f", pub), T0: "0", Repo: "0", Interest: "0", ModelInterest: "0", Net: fmt.Sprintf("%.2f", pub), Residual: "0", Other: fmt.Sprintf("%.2f", other), Comparison: fmt.Sprintf("%.2f", -other), ModelStrategies: map[string]string{"etf_cross_section": fmt.Sprintf("%.2f", pub)}, Strategies: map[string]string{"etf_cross_section": fmt.Sprintf("%.2f", pub)}, Parts: []AttributionPart{}}
		if i == 0 {
			r.Model = "100"
			r.T0 = "3"
			r.ModelStrategies["etf_cross_section"] = "100"
			r.Strategies["etf_cross_section"] = "100"
			r.Strategies["etf_redemption_t0"] = "3"
			r.Parts = []AttributionPart{p}
		}
		a.Days = append(a.Days, r)
	}
	s.Attribution = a
	return s
}

func TestCycleReviewAttributionValidation(t *testing.T) {
	s := attributionFixture(t)
	v, err := BuildCycleReviewVersion(s, "v5", strings.Repeat("a", 64))
	if err != nil {
		t.Fatal(err)
	}
	for _, kind := range []string{"account", "final", "write", "hash", "duplicate-part", "cycle-net", "part-day", "missing-day", "money", "nan", "precision", "bound", "published", "model", "strategy", "residual", "other", "asset", "repo", "fee"} {
		t.Run(kind, func(t *testing.T) {
			var d CycleReviewDocument
			_ = json.Unmarshal(v.Payload, &d)
			a := d.Attribution
			r := &a.Days[0]
			switch kind {
			case "account":
				a.AccountID = "other"
			case "final":
				a.Final = true
			case "write":
				a.Writes = 1
			case "hash":
				a.SourceSHA = "bad"
			case "duplicate-part":
				a.Parts = append(a.Parts, a.Parts[0])
			case "cycle-net":
				a.Parts[0].Net = "4"
				a.Parts[0].Correction = "4"
			case "part-day":
				a.Parts[0].Date = "2026-11-01"
			case "missing-day":
				a.Days = a.Days[:4]
			case "money":
				r.Net = "1e2"
			case "nan":
				r.Net = "NaN"
			case "precision":
				r.Net = "103.0000001"
			case "bound":
				d.Daily.Days[0].Account.PublishedPnLCents = 100000000000000000
			case "published":
				r.Published = "102"
			case "model":
				r.Model = "99"
			case "strategy":
				r.Strategies["etf_cross_section"] = "103"
				r.Strategies["etf_redemption_t0"] = "0"
			case "residual":
				r.Residual = "1"
			case "other":
				r.Other = "1"
			case "asset":
				r.AssetVerified = true
			case "repo":
				r.Interest = "1"
			case "fee":
				r.RepoFee = "-1"
			}
			if ValidateCycleReview(d) == nil {
				t.Fatal("invalid attribution accepted")
			}
		})
	}
}

func TestCycleReviewAttributionPrivateEvidence(t *testing.T) {
	root := "../../outputs/recovery/"
	b, err := os.ReadFile(root + "fuying13_cycle_review_20261115/profit-v13.json")
	if os.IsNotExist(err) {
		t.Skip("private evidence absent")
	}
	if err != nil {
		t.Fatal(err)
	}
	var s CycleReviewSource
	if err = json.Unmarshal(b, &s); err != nil {
		t.Fatal(err)
	}
	b, err = os.ReadFile(root + "fuying13_daily_attribution_20261115/review-v6.json")
	if os.IsNotExist(err) {
		t.Skip("private audit absent")
	}
	if err != nil {
		t.Fatal(err)
	}
	var a AttributionReview
	if err = json.Unmarshal(b, &a); err != nil {
		t.Fatal(err)
	}
	a.SourceSHA = "b2bfcd75f7aa797cc6272bda21b9a48ef7d8990038ee885c530b7b60cb0544f8"
	s.Attribution = &a
	v, err := BuildCycleReviewVersion(s, "v5", strings.Repeat("a", 64))
	if err != nil {
		t.Fatal(err)
	}
	var d CycleReviewDocument
	_ = json.Unmarshal(v.Payload, &d)
	if len(d.Attribution.Days) != 25 || len(d.Attribution.Statements.Days) != 4 {
		t.Fatal("coverage lost")
	}
	for _, kind := range []string{"statement", "final", "fee", "duplicate", "unexplained"} {
		_ = json.Unmarshal(v.Payload, &d)
		x := &d.Attribution.Statements.Days[0]
		switch kind {
		case "statement":
			x.PnL = "1"
		case "final":
			x.Final = true
		case "fee":
			x.Fee = "0"
		case "duplicate":
			d.Attribution.Statements.Days = append(d.Attribution.Statements.Days, *x)
		case "unexplained":
			x.Unexplained = "0.01"
		}
		if ValidateCycleReview(d) == nil {
			t.Fatalf("invalid statement accepted: %s", kind)
		}
	}
}
