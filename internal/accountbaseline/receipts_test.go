package accountbaseline

import (
	"testing"

	"ti-relay-trader/internal/ledger"
)

func linkedReply(r ledger.CurrentBaselineArchive) *ledger.CurrentBaselineReplyReceipt {
	body := []byte(" \n" + r.Body + "\n")
	return &ledger.CurrentBaselineReplyReceipt{Status: "matched", ArchiveMatch: "legacy_trimmed", OriginalBody: body, OriginalSHA256: digest(body), Observations: []ledger.CurrentBaselineReceiptReference{{Outcome: "applied", BodySHA256: digest(body), BodyBytes: len(body)}}}
}

func TestCurrentAccountBaselineReceiptProjectionPolicy(t *testing.T) {
	for _, status := range []string{"matched", "unavailable", "receiver_missing", "before_capture", "missing", "binding_mismatch", "protection_unavailable", "scope_mismatch", "not_applied", "conflict", "archive_mismatch", "unknown"} {
		t.Run(status, func(t *testing.T) {
			s := fixtureSources(t, 1)
			q := s.Queries["positions"]
			for i := range q.Replies {
				q.Replies[i].Receipt = linkedReply(q.Replies[i])
				q.Replies[i].Receipt.Status = status
			}
			s.Queries["positions"] = q
			c, err := Assemble(s, "policy", fixtureStart)
			if err != nil {
				t.Fatal(err)
			}
			p := reports(t, c)["positions"]
			want := status == "matched" || status == "unavailable" || status == "receiver_missing" || status == "before_capture"
			if p.ProjectionValid != want || p.RetainedPagesReceipted != (status == "matched") || p.ArchiveComplete {
				t.Fatal(p)
			}
			if status == "matched" {
				page := p.Pages[0]
				if page.BodySource != "durable_receipt" || page.BodySHA256 != q.Replies[0].Receipt.OriginalSHA256 || page.ArchiveBodySHA256 != digest([]byte(q.Replies[0].Body)) || page.BodySHA256 == page.ArchiveBodySHA256 {
					t.Fatal("digests conflated", page)
				}
			}
		})
	}
	for _, fault := range []string{"hash", "body", "no_applied", "double_applied", "relation"} {
		t.Run(fault, func(t *testing.T) {
			s := fixtureSources(t, 1)
			q := s.Queries["positions"]
			q.Replies[0].Receipt = linkedReply(q.Replies[0])
			l := q.Replies[0].Receipt
			switch fault {
			case "hash":
				l.OriginalSHA256 = "bad"
			case "body":
				l.OriginalBody = nil
			case "no_applied":
				l.Observations = nil
			case "double_applied":
				l.Observations = append(l.Observations, l.Observations[0])
			case "relation":
				l.ArchiveMatch = "unknown"
			}
			s.Queries["positions"] = q
			c, err := Assemble(s, fault, fixtureStart)
			if err != nil {
				t.Fatal(err)
			}
			p := reports(t, c)["positions"]
			if p.ProjectionValid || !hasFlag(p, "reply_receipt_invalid_link") {
				t.Fatal(p)
			}
		})
	}
}
