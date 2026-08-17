package main

import "testing"

func TestPerformanceResultPublishable(t *testing.T) {
	tests := []struct {
		name       string
		costStatus string
		navStatus  string
		want       bool
	}{
		{name: "calculated provisional", costStatus: "calculated", navStatus: "provisional", want: true},
		{name: "estimated provisional", costStatus: "estimated", navStatus: "provisional", want: true},
		{name: "blocked cost", costStatus: "blocked", navStatus: "provisional", want: false},
		{name: "blocked nav", costStatus: "calculated", navStatus: "blocked", want: false},
		{name: "missing statuses", costStatus: "", navStatus: "", want: false},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := performanceResultPublishable(test.costStatus, test.navStatus); got != test.want {
				t.Fatalf("performanceResultPublishable(%q, %q) = %v, want %v", test.costStatus, test.navStatus, got, test.want)
			}
		})
	}
}
