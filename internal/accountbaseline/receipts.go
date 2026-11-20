package accountbaseline

import (
	"strings"

	"ti-relay-trader/internal/ledger"
)

func querySourceBody(r ledger.CurrentBaselineArchive) []byte {
	if r.Receipt != nil && r.Receipt.Status == "matched" {
		return r.Receipt.OriginalBody
	}
	return []byte(r.Body)
}

// Legacy-only evidence can still produce a reference view. Contradictory new
// receipts must not fall back to a successful-looking mutable archive page.
func reviewQueryReceipt(p *QueryReport, r ledger.CurrentBaselineArchive) (linked, usable bool) {
	l := r.Receipt
	if l == nil {
		p.flag("reply_receipt_unavailable")
		return false, true
	}
	if l.Status != "matched" {
		p.flag("reply_receipt_" + l.Status)
		switch l.Status {
		case "unavailable", "receiver_missing", "before_capture":
			return false, true
		default:
			return false, false
		}
	}
	applied := 0
	valid := len(l.OriginalBody) > 0 && digest(l.OriginalBody) == l.OriginalSHA256
	switch l.ArchiveMatch {
	case "exact":
		valid = valid && string(l.OriginalBody) == r.Body
	case "legacy_trimmed":
		valid = valid && strings.TrimSpace(string(l.OriginalBody)) == r.Body
	default:
		valid = false
	}
	for _, ref := range l.Observations {
		valid = valid && ref.BodySHA256 == l.OriginalSHA256 && ref.BodyBytes == len(l.OriginalBody)
		if ref.Outcome == "applied" {
			applied++
		} else {
			p.flag("reply_receipt_prior_failure")
		}
	}
	if !valid || applied != 1 {
		p.flag("reply_receipt_invalid_link")
		return false, false
	}
	return true, true
}
