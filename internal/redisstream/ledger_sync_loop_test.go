package redisstream

import (
	"reflect"
	"testing"
)

func TestXReadStreamsBuildsSingleMultiStreamRequest(t *testing.T) {
	cursors := []ledgerStreamCursor{
		{name: "relay:a:reply", role: SuffixReply, lastID: "100-0"},
		{name: "relay:a:event", role: SuffixEvent, lastID: "200-0"},
		{name: "relay:b:reply", role: SuffixReply, lastID: "300-0"},
	}

	got := xReadStreams(cursors)
	want := []string{
		"relay:a:reply",
		"relay:a:event",
		"relay:b:reply",
		"100-0",
		"200-0",
		"300-0",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("xReadStreams() = %#v, want %#v", got, want)
	}
}

func TestXReadStreamsHandlesNoCursors(t *testing.T) {
	if got := xReadStreams(nil); len(got) != 0 {
		t.Fatalf("xReadStreams(nil) = %#v, want empty", got)
	}
}
